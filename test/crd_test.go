package test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// processCRDFile validates a single CRD file and updates counters and error list
func processCRDFile(t *testing.T, path string, info os.FileInfo, totalCRDs *int, validCRDs *int, errors *[]string) error {
	if info.IsDir() || filepath.Ext(path) != ".yaml" {
		return nil
	}

	*totalCRDs++

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		*errors = append(*errors, fmt.Sprintf("%s: failed to read: %v", filepath.Base(path), err))
		return nil
	}

	// Parse as CRD
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(data, crd); err != nil {
		*errors = append(*errors, fmt.Sprintf("%s: failed to parse: %v", filepath.Base(path), err))
		return nil
	}

	// Basic validation
	if err := validateCRD(crd, filepath.Base(path), errors); err != nil {
		return nil
	}

	*validCRDs++
	t.Logf("✓ %s: %s.%s (%d versions)", filepath.Base(path), crd.Spec.Names.Kind, crd.Spec.Group, len(crd.Spec.Versions))

	return nil
}

// validateCRD performs basic validation on a CRD
func validateCRD(crd *apiextensionsv1.CustomResourceDefinition, filename string, errors *[]string) error {
	if crd.Spec.Group == "" {
		*errors = append(*errors, fmt.Sprintf("%s: missing spec.group", filename))
		return fmt.Errorf("missing spec.group")
	}
	if len(crd.Spec.Versions) == 0 {
		*errors = append(*errors, fmt.Sprintf("%s: missing spec.versions", filename))
		return fmt.Errorf("missing spec.versions")
	}
	if crd.Spec.Names.Kind == "" {
		*errors = append(*errors, fmt.Sprintf("%s: missing spec.names.kind", filename))
		return fmt.Errorf("missing spec.names.kind")
	}
	return nil
}

// TestCRDsCanBeLoaded verifies that all CRD files in assets/crds can be parsed
// This ensures CRDs are valid YAML and conform to the CustomResourceDefinition schema
func TestCRDsCanBeLoaded(t *testing.T) {
	crdsDir := filepath.Join("..", "assets", "crds")

	// Verify directory exists
	if _, err := os.Stat(crdsDir); os.IsNotExist(err) {
		t.Fatalf("CRDs directory does not exist: %s", crdsDir)
	}

	// Walk all CRD directories
	crdDirs := []string{
		filepath.Join(crdsDir, "kubevirt"),
		filepath.Join(crdsDir, "openshift"),
		filepath.Join(crdsDir, "remediation"),
		filepath.Join(crdsDir, "operators"),
		filepath.Join(crdsDir, "observability"),
		filepath.Join(crdsDir, "oadp"),
	}

	totalCRDs := 0
	validCRDs := 0
	var errors []string

	for _, dir := range crdDirs {
		// Skip if directory doesn't exist
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		// Find all YAML files in directory
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return processCRDFile(t, path, info, &totalCRDs, &validCRDs, &errors)
		})

		if err != nil {
			t.Errorf("Failed to walk directory %s: %v", dir, err)
		}
	}

	// Report results
	reportResults(t, totalCRDs, validCRDs, errors)
}

// reportResults logs the validation summary and handles errors
func reportResults(t *testing.T, totalCRDs, validCRDs int, errors []string) {
	t.Logf("\nCRD Validation Summary:")
	t.Logf("  Total CRD files: %d", totalCRDs)
	t.Logf("  Valid CRDs: %d", validCRDs)
	t.Logf("  Invalid CRDs: %d", len(errors))

	if len(errors) > 0 {
		t.Errorf("\nValidation errors:")
		for _, err := range errors {
			t.Errorf("  ✗ %s", err)
		}
		t.Fatalf("CRD validation failed with %d errors", len(errors))
	}

	if totalCRDs == 0 {
		t.Fatal("No CRD files found - run 'make update-crds' first")
	}

	t.Logf("\n✓ All %d CRDs are valid!", validCRDs)
}

// TestCRDDirectoryStructure verifies the expected directory structure exists
func TestCRDDirectoryStructure(t *testing.T) {
	crdsDir := filepath.Join("..", "assets", "crds")

	expectedDirs := []string{
		"kubevirt",
		"openshift",
		"remediation",
		"operators",
	}

	for _, dir := range expectedDirs {
		path := filepath.Join(crdsDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Expected directory does not exist: %s", path)
		} else {
			t.Logf("✓ Directory exists: %s", dir)
		}
	}
}

// TestREADMEExists verifies the CRD README exists
func TestREADMEExists(t *testing.T) {
	readmePath := filepath.Join("..", "assets", "crds", "README.md")

	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		t.Errorf("README.md does not exist at %s", readmePath)
		t.Log("Run 'make update-crds' to generate it")
	} else {
		t.Logf("✓ README.md exists")

		// Check if it's auto-generated
		data, err := os.ReadFile(readmePath)
		if err != nil {
			t.Errorf("Failed to read README: %v", err)
			return
		}

		content := string(data)
		if len(content) < 100 {
			t.Errorf("README seems too short (%d bytes) - may be empty", len(content))
		} else {
			t.Logf("✓ README.md has content (%d bytes)", len(content))
		}
	}
}
