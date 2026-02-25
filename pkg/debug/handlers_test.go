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

package debug

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	pkgrender "github.com/kubevirt/virt-platform-autopilot/pkg/render"
)

func TestHandleHealth(t *testing.T) {
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(nil, loader, registry)

	req := httptest.NewRequest(http.MethodGet, "/debug/health", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK\n", w.Body.String())
}

func TestHandleRender(t *testing.T) {
	// Create fake HCO
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(pkgcontext.HCOGVK)
	hco.SetName("kubevirt-hyperconverged")
	hco.SetNamespace("openshift-cnv")
	hco.SetAnnotations(map[string]string{
		"platform.kubevirt.io/managed-by": "virt-platform-autopilot",
	})

	// Create fake client with HCO
	fakeClient := fake.NewClientBuilder().
		WithObjects(hco).
		Build()

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(fakeClient, loader, registry)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
		checkResponse  func(t *testing.T, body string)
	}{
		{
			name:           "default yaml format",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body string) {
				assert.Contains(t, body, "# Asset:")
				assert.Contains(t, body, "# Status:")
				assert.Contains(t, body, "---")
			},
		},
		{
			name:           "json format",
			queryParams:    "?format=json",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body string) {
				var outputs []pkgrender.RenderOutput
				err := json.Unmarshal([]byte(body), &outputs)
				assert.NoError(t, err)
				assert.NotEmpty(t, outputs)
			},
		},
		{
			name:           "show excluded",
			queryParams:    "?show-excluded=true",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body string) {
				// Multi-document YAML: check for the EXCLUDED status comment header
				assert.Contains(t, body, "# Status: EXCLUDED", "Should have excluded assets when show-excluded=true")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/debug/render"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			server.handleRender(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.String())
			}
		})
	}
}

func TestHandleRenderAsset(t *testing.T) {
	// Create fake HCO
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(pkgcontext.HCOGVK)
	hco.SetName("kubevirt-hyperconverged")
	hco.SetNamespace("openshift-cnv")

	fakeClient := fake.NewClientBuilder().
		WithObjects(hco).
		Build()

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(fakeClient, loader, registry)

	tests := []struct {
		name           string
		assetName      string
		expectedStatus int
		checkResponse  func(t *testing.T, body string)
	}{
		{
			name:           "valid asset",
			assetName:      "swap-enable",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body string) {
				assert.Contains(t, body, "# Asset: swap-enable")
				assert.Contains(t, body, "---")
			},
		},
		{
			name:           "invalid asset",
			assetName:      "nonexistent-asset",
			expectedStatus: http.StatusNotFound,
			checkResponse:  nil,
		},
		{
			name:           "empty asset name",
			assetName:      "",
			expectedStatus: http.StatusBadRequest,
			checkResponse:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/debug/render/"+tt.assetName, nil)
			w := httptest.NewRecorder()

			server.handleRenderAsset(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.String())
			}
		})
	}
}

func TestHandleExclusions(t *testing.T) {
	// Create fake HCO
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(pkgcontext.HCOGVK)
	hco.SetName("kubevirt-hyperconverged")
	hco.SetNamespace("openshift-cnv")

	fakeClient := fake.NewClientBuilder().
		WithObjects(hco).
		Build()

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(fakeClient, loader, registry)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
		checkResponse  func(t *testing.T, body string)
	}{
		{
			name:           "yaml format",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body string) {
				var exclusions []ExclusionInfo
				err := yaml.Unmarshal([]byte(body), &exclusions)
				assert.NoError(t, err)
				// Should have some exclusions (opt-in assets)
				assert.NotEmpty(t, exclusions)
			},
		},
		{
			name:           "json format",
			queryParams:    "?format=json",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body string) {
				var exclusions []ExclusionInfo
				err := json.Unmarshal([]byte(body), &exclusions)
				assert.NoError(t, err)
				assert.NotEmpty(t, exclusions)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/debug/exclusions"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			server.handleExclusions(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.String())
			}
		})
	}
}

func TestHandleTombstones(t *testing.T) {
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(nil, loader, registry)

	req := httptest.NewRequest(http.MethodGet, "/debug/tombstones", nil)
	w := httptest.NewRecorder()

	server.handleTombstones(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Should return empty list or valid tombstones
	var tombstones []TombstoneInfo
	err = yaml.Unmarshal(w.Body.Bytes(), &tombstones)
	assert.NoError(t, err)
	// Currently no tombstones in repo, so should be empty
	assert.Empty(t, tombstones)
}

func TestGetRenderContext(t *testing.T) {
	// Create fake HCO
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(pkgcontext.HCOGVK)
	hco.SetName("kubevirt-hyperconverged")
	hco.SetNamespace("openshift-cnv")

	fakeClient := fake.NewClientBuilder().
		WithObjects(hco).
		Build()

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(fakeClient, loader, registry)

	ctx := context.Background()
	renderCtx, err := server.getRenderContext(ctx)

	assert.NoError(t, err)
	assert.NotNil(t, renderCtx)
	assert.Equal(t, "kubevirt-hyperconverged", renderCtx.HCO.GetName())
}

func TestGetRenderContextNoHCO(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(fakeClient, loader, registry)

	ctx := context.Background()
	_, err = server.getRenderContext(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no HyperConverged resources found")
}

func TestMethodNotAllowed(t *testing.T) {
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	require.NoError(t, err)

	server := NewServer(nil, loader, registry)

	endpoints := []string{
		"/debug/render",
		"/debug/exclusions",
		"/debug/tombstones",
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, endpoint, nil)
			w := httptest.NewRecorder()

			mux := http.NewServeMux()
			server.InstallHandlers(mux)
			mux.ServeHTTP(w, req)

			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}
