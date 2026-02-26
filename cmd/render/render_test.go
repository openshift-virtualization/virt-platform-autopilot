/*
Copyright 2026 The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	pkgrender "github.com/kubevirt/virt-platform-autopilot/pkg/render"
)

func TestLoadHCOFromFile(t *testing.T) {
	// Create temp HCO file
	hcoYAML := `apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
spec:
  featureGates:
    deployKubeSecondaryDNS: true
`

	tmpDir := t.TempDir()
	hcoPath := filepath.Join(tmpDir, "hco.yaml")
	err := os.WriteFile(hcoPath, []byte(hcoYAML), 0644)
	require.NoError(t, err)

	hco, err := loadHCOFromFile(hcoPath)
	assert.NoError(t, err)
	assert.NotNil(t, hco)
	assert.Equal(t, "HyperConverged", hco.GetKind())
	assert.Equal(t, "kubevirt-hyperconverged", hco.GetName())
}

func TestLoadHCOFromFileInvalid(t *testing.T) {
	// Create temp file with non-HCO content
	notHCO := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "notHCO.yaml")
	err := os.WriteFile(path, []byte(notHCO), 0644)
	require.NoError(t, err)

	_, err = loadHCOFromFile(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected kind HyperConverged")
}

func TestLoadHCOFromFileNotFound(t *testing.T) {
	_, err := loadHCOFromFile("/nonexistent/path.yaml")
	assert.Error(t, err)
}

func TestCheckConditions(t *testing.T) {
	hco := pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv")
	hco.SetAnnotations(map[string]string{
		"platform.kubevirt.io/enable-metallb": "true",
		"platform.kubevirt.io/openshift":      "true",
	})
	renderCtx := pkgcontext.NewRenderContext(hco)

	tests := []struct {
		name       string
		asset      *assets.AssetMetadata
		shouldPass bool
	}{
		{
			name: "no conditions",
			asset: &assets.AssetMetadata{
				Name:       "test",
				Conditions: []assets.AssetCondition{},
			},
			shouldPass: true,
		},
		{
			name: "annotation condition met",
			asset: &assets.AssetMetadata{
				Name: "test",
				Conditions: []assets.AssetCondition{
					{
						Type:  assets.ConditionTypeAnnotation,
						Key:   "platform.kubevirt.io/enable-metallb",
						Value: "true",
					},
				},
			},
			shouldPass: true,
		},
		{
			name: "annotation condition not met",
			asset: &assets.AssetMetadata{
				Name: "test",
				Conditions: []assets.AssetCondition{
					{
						Type:  assets.ConditionTypeAnnotation,
						Key:   "platform.kubevirt.io/enable-aaq",
						Value: "true",
					},
				},
			},
			shouldPass: false,
		},
		{
			name: "hardware detection always fails",
			asset: &assets.AssetMetadata{
				Name: "test",
				Conditions: []assets.AssetCondition{
					{
						Type:     assets.ConditionTypeHardwareDetection,
						Detector: "numaNodesPresent",
					},
				},
			},
			shouldPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pkgrender.CheckConditions(tt.asset, renderCtx)
			assert.Equal(t, tt.shouldPass, result)
		})
	}
}

func TestWriteYAMLOutput(t *testing.T) {
	outputs := []pkgrender.RenderOutput{
		{
			Asset:     "test-asset",
			Path:      "test/path.yaml",
			Component: "TestComponent",
			Status:    "INCLUDED",
			Object:    pkgcontext.NewMockHCO("test", "default"),
		},
		{
			Asset:     "excluded-asset",
			Path:      "test/excluded.yaml",
			Component: "ExcludedComponent",
			Status:    "EXCLUDED",
			Reason:    "Conditions not met",
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := writeOutput(outputs, "yaml")
	assert.NoError(t, err)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	// Verify output contains asset metadata
	assert.Contains(t, output, "# Asset: test-asset")
	assert.Contains(t, output, "# Status: INCLUDED")
	assert.Contains(t, output, "# Asset: excluded-asset")
	assert.Contains(t, output, "# Reason: Conditions not met")
	assert.Contains(t, output, "---")
}

func TestWriteJSONOutput(t *testing.T) {
	outputs := []pkgrender.RenderOutput{
		{
			Asset:     "test-asset",
			Path:      "test/path.yaml",
			Component: "TestComponent",
			Status:    "INCLUDED",
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := writeOutput(outputs, "json")
	assert.NoError(t, err)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	// Verify JSON structure
	assert.Contains(t, output, `"asset": "test-asset"`)
	assert.Contains(t, output, `"status": "INCLUDED"`)
}

func TestWriteStatusOutput(t *testing.T) {
	outputs := []pkgrender.RenderOutput{
		{
			Asset:     "included-asset",
			Component: "Component1",
			Status:    "INCLUDED",
		},
		{
			Asset:     "excluded-asset",
			Component: "Component2",
			Status:    "EXCLUDED",
			Reason:    "Conditions not met",
		},
		{
			Asset:     "filtered-asset",
			Component: "Component3",
			Status:    "FILTERED",
			Reason:    "Root exclusion",
		},
		{
			Asset:     "error-asset",
			Component: "Component4",
			Status:    "ERROR",
			Reason:    "Parse error",
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := writeStatusOutput(outputs)
	assert.NoError(t, err)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	// Verify table structure
	assert.Contains(t, output, "ASSET")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "COMPONENT")
	assert.Contains(t, output, "REASON")

	// Verify summary
	assert.Contains(t, output, "Summary:")
	assert.Contains(t, output, "1 included")
	assert.Contains(t, output, "1 excluded")
	assert.Contains(t, output, "1 filtered")
	assert.Contains(t, output, "1 errors")
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exact length", 12, "exact length"},
		{"this is a very long string", 10, "this is..."},
		{"", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := truncate(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), tt.maxLen)
		})
	}
}

func TestRenderCommandFlags(t *testing.T) {
	cmd := NewRenderCommand()

	// Verify command structure
	assert.Equal(t, "render", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)

	// Verify flags exist
	flags := cmd.Flags()
	assert.NotNil(t, flags.Lookup("kubeconfig"))
	assert.NotNil(t, flags.Lookup("hco-file"))
	assert.NotNil(t, flags.Lookup("asset"))
	assert.NotNil(t, flags.Lookup("show-excluded"))
	assert.NotNil(t, flags.Lookup("output"))
}

func TestRunRenderValidation(t *testing.T) {
	// Create temp HCO file
	hcoYAML := `apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
`

	tmpDir := t.TempDir()
	hcoPath := filepath.Join(tmpDir, "hco.yaml")
	err := os.WriteFile(hcoPath, []byte(hcoYAML), 0644)
	require.NoError(t, err)

	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "no kubeconfig or hco-file",
			args:        []string{},
			expectError: true,
			errorMsg:    "either --kubeconfig or --hco-file must be specified",
		},
		{
			name:        "both kubeconfig and hco-file",
			args:        []string{"--kubeconfig=/path/to/kubeconfig", "--hco-file=" + hcoPath},
			expectError: true,
			errorMsg:    "mutually exclusive",
		},
		{
			name:        "valid hco-file",
			args:        []string{"--hco-file=" + hcoPath, "--output=status"},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRenderCommand()
			cmd.SetArgs(tt.args)

			err := cmd.Execute()

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				// In non-error case, we expect it to run successfully
				// It may output to stdout which we can ignore
				if err != nil {
					// Check it's not a validation error
					assert.NotContains(t, err.Error(), "must be specified")
					assert.NotContains(t, err.Error(), "mutually exclusive")
				}
			}
		})
	}
}

func TestWriteOutputUnsupportedFormat(t *testing.T) {
	outputs := []pkgrender.RenderOutput{}
	err := writeOutput(outputs, "unsupported")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported output format")
}

func TestRenderWithAssetFilter(t *testing.T) {
	// This is more of an integration test but tests the asset filter logic
	hco := pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv")
	renderCtx := pkgcontext.NewRenderContext(hco)

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	// Get a specific asset
	asset, err := registry.GetAsset("swap-enable")
	require.NoError(t, err)

	// Verify conditions check
	shouldInclude := pkgrender.CheckConditions(asset, renderCtx)
	assert.True(t, shouldInclude, "swap-enable should have no conditions")
}

func TestRenderOutputFormats(t *testing.T) {
	// Test all output formats work
	outputs := []pkgrender.RenderOutput{
		{
			Asset:     "test",
			Path:      "test.yaml",
			Component: "Test",
			Status:    "INCLUDED",
		},
	}

	formats := []string{"yaml", "json", "status"}
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			// Capture output
			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err := writeOutput(outputs, format)
			assert.NoError(t, err)

			w.Close()
			os.Stdout = old

			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			output := buf.String()

			// Basic validation that something was written
			assert.NotEmpty(t, output)

			// Format-specific checks
			switch format {
			case "yaml":
				assert.Contains(t, output, "# Asset:")
			case "json":
				assert.True(t, strings.HasPrefix(strings.TrimSpace(output), "["))
			case "status":
				assert.Contains(t, output, "Summary:")
			}
		})
	}
}
