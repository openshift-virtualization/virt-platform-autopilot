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
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

// Server provides debug endpoints for the controller
type Server struct {
	client   client.Client
	loader   *assets.Loader
	registry *assets.Registry
	renderer *engine.Renderer
}

// NewServer creates a new debug server
func NewServer(c client.Client, loader *assets.Loader, registry *assets.Registry) *Server {
	return &Server{
		client:   c,
		loader:   loader,
		registry: registry,
		renderer: engine.NewRenderer(loader),
	}
}

// InstallHandlers registers debug HTTP handlers
func (s *Server) InstallHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/render", s.handleRender)
	mux.HandleFunc("/debug/render/", s.handleRenderAsset) // Trailing slash for path params
	mux.HandleFunc("/debug/exclusions", s.handleExclusions)
	mux.HandleFunc("/debug/tombstones", s.handleTombstones)
	mux.HandleFunc("/debug/health", s.handleHealth)
}

// RenderOutput represents the output format for rendered assets
type RenderOutput struct {
	Asset      string                     `json:"asset" yaml:"asset"`
	Path       string                     `json:"path" yaml:"path"`
	Component  string                     `json:"component" yaml:"component"`
	Status     string                     `json:"status" yaml:"status"`
	Reason     string                     `json:"reason,omitempty" yaml:"reason,omitempty"`
	Conditions []assets.AssetCondition    `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Object     *unstructured.Unstructured `json:"object,omitempty" yaml:"object,omitempty"`
}

// handleRender renders all assets and returns them
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Parse query parameters
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}
	showExcluded := r.URL.Query().Get("show-excluded") == "true"

	// Get HCO for render context
	renderCtx, err := s.getRenderContext(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get render context: %v", err), http.StatusInternalServerError)
		return
	}

	// Render all assets
	outputs := []RenderOutput{}
	assetList := s.registry.ListAssetsByReconcileOrder()

	for _, assetMeta := range assetList {
		output := RenderOutput{
			Asset:      assetMeta.Name,
			Path:       assetMeta.Path,
			Component:  assetMeta.Component,
			Conditions: assetMeta.Conditions,
		}

		// Check conditions
		if !s.checkConditions(&assetMeta, renderCtx) {
			output.Status = "EXCLUDED"
			output.Reason = "Conditions not met"
			if showExcluded {
				outputs = append(outputs, output)
			}
			continue
		}

		// Render asset
		rendered, err := s.renderer.RenderAsset(&assetMeta, renderCtx)
		if err != nil {
			output.Status = "ERROR"
			output.Reason = err.Error()
			outputs = append(outputs, output)
			continue
		}

		if rendered == nil {
			output.Status = "EXCLUDED"
			output.Reason = "Conditional template rendered empty"
			if showExcluded {
				outputs = append(outputs, output)
			}
			continue
		}

		// Check root exclusion
		disabledAnnotation := renderCtx.HCO.GetAnnotations()[engine.DisabledResourcesAnnotation]
		if disabledAnnotation != "" {
			disabledMap := engine.ParseDisabledResources(disabledAnnotation)
			if engine.IsResourceExcluded(rendered.GetKind(), rendered.GetName(), disabledMap) {
				output.Status = "FILTERED"
				output.Reason = "Root exclusion (disabled-resources annotation)"
				if showExcluded {
					outputs = append(outputs, output)
				}
				continue
			}
		}

		output.Status = "INCLUDED"
		output.Object = rendered
		outputs = append(outputs, output)
	}

	// Write response
	s.writeResponse(w, outputs, format)
}

// handleRenderAsset renders a specific asset by name
func (s *Server) handleRenderAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract asset name from path: /debug/render/{asset}
	path := strings.TrimPrefix(r.URL.Path, "/debug/render/")
	assetName := strings.TrimSpace(path)

	if assetName == "" {
		http.Error(w, "Asset name required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}

	// Get asset metadata
	assetMeta, err := s.registry.GetAsset(assetName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Asset not found: %v", err), http.StatusNotFound)
		return
	}

	// Get render context
	renderCtx, err := s.getRenderContext(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get render context: %v", err), http.StatusInternalServerError)
		return
	}

	output := RenderOutput{
		Asset:      assetMeta.Name,
		Path:       assetMeta.Path,
		Component:  assetMeta.Component,
		Conditions: assetMeta.Conditions,
	}

	// Check conditions
	if !s.checkConditions(assetMeta, renderCtx) {
		output.Status = "EXCLUDED"
		output.Reason = "Conditions not met"
		s.writeResponse(w, []RenderOutput{output}, format)
		return
	}

	// Render asset
	rendered, err := s.renderer.RenderAsset(assetMeta, renderCtx)
	if err != nil {
		output.Status = "ERROR"
		output.Reason = err.Error()
		s.writeResponse(w, []RenderOutput{output}, format)
		return
	}

	if rendered == nil {
		output.Status = "EXCLUDED"
		output.Reason = "Conditional template rendered empty"
		s.writeResponse(w, []RenderOutput{output}, format)
		return
	}

	// Check root exclusion
	disabledAnnotation := renderCtx.HCO.GetAnnotations()[engine.DisabledResourcesAnnotation]
	if disabledAnnotation != "" {
		disabledMap := engine.ParseDisabledResources(disabledAnnotation)
		if engine.IsResourceExcluded(rendered.GetKind(), rendered.GetName(), disabledMap) {
			output.Status = "FILTERED"
			output.Reason = "Root exclusion (disabled-resources annotation)"
			s.writeResponse(w, []RenderOutput{output}, format)
			return
		}
	}

	output.Status = "INCLUDED"
	output.Object = rendered
	s.writeResponse(w, []RenderOutput{output}, format)
}

// ExclusionInfo represents information about excluded assets
type ExclusionInfo struct {
	Asset     string                `json:"asset" yaml:"asset"`
	Path      string                `json:"path" yaml:"path"`
	Component string                `json:"component" yaml:"component"`
	Reason    string                `json:"reason" yaml:"reason"`
	Details   map[string]string     `json:"details,omitempty" yaml:"details,omitempty"`
	Metadata  *assets.AssetMetadata `json:"-" yaml:"-"`
}

// handleExclusions shows all excluded/filtered assets
func (s *Server) handleExclusions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}

	// Get render context
	renderCtx, err := s.getRenderContext(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get render context: %v", err), http.StatusInternalServerError)
		return
	}

	// Find all excluded assets
	exclusions := []ExclusionInfo{}
	assetList := s.registry.ListAssetsByReconcileOrder()

	for _, assetMeta := range assetList {
		// Check conditions
		if !s.checkConditions(&assetMeta, renderCtx) {
			exclusion := ExclusionInfo{
				Asset:     assetMeta.Name,
				Path:      assetMeta.Path,
				Component: assetMeta.Component,
				Reason:    "Conditions not met",
				Details:   s.getConditionDetails(&assetMeta, renderCtx),
				Metadata:  &assetMeta,
			}
			exclusions = append(exclusions, exclusion)
			continue
		}

		// Try rendering
		rendered, err := s.renderer.RenderAsset(&assetMeta, renderCtx)
		if err != nil || rendered == nil {
			reason := "Template rendered empty"
			if err != nil {
				reason = fmt.Sprintf("Render error: %v", err)
			}
			exclusion := ExclusionInfo{
				Asset:     assetMeta.Name,
				Path:      assetMeta.Path,
				Component: assetMeta.Component,
				Reason:    reason,
				Metadata:  &assetMeta,
			}
			exclusions = append(exclusions, exclusion)
			continue
		}

		// Check root exclusion
		disabledAnnotation := renderCtx.HCO.GetAnnotations()[engine.DisabledResourcesAnnotation]
		if disabledAnnotation != "" {
			disabledMap := engine.ParseDisabledResources(disabledAnnotation)
			if engine.IsResourceExcluded(rendered.GetKind(), rendered.GetName(), disabledMap) {
				exclusion := ExclusionInfo{
					Asset:     assetMeta.Name,
					Path:      assetMeta.Path,
					Component: assetMeta.Component,
					Reason:    "Root exclusion",
					Details: map[string]string{
						"annotation": engine.DisabledResourcesAnnotation,
						"value":      disabledAnnotation,
						"resource":   fmt.Sprintf("%s/%s", rendered.GetKind(), rendered.GetName()),
					},
					Metadata: &assetMeta,
				}
				exclusions = append(exclusions, exclusion)
			}
		}
	}

	s.writeResponse(w, exclusions, format)
}

// TombstoneInfo represents information about tombstones
type TombstoneInfo struct {
	Kind      string `json:"kind" yaml:"kind"`
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Path      string `json:"path" yaml:"path"`
}

// handleTombstones lists all tombstones
func (s *Server) handleTombstones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}

	// Load tombstones
	tombstones, err := s.loader.LoadTombstones()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load tombstones: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert to info
	infos := make([]TombstoneInfo, len(tombstones))
	for i, ts := range tombstones {
		infos[i] = TombstoneInfo{
			Kind:      ts.GVK.Kind,
			Name:      ts.Name,
			Namespace: ts.Namespace,
			Path:      ts.Path,
		}
	}

	s.writeResponse(w, infos, format)
}

// handleHealth is a simple health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK\n")
}

// getRenderContext builds a render context from the cluster HCO
func (s *Server) getRenderContext(ctx context.Context) (*pkgcontext.RenderContext, error) {
	// Get HCO from cluster
	hcoList := &unstructured.UnstructuredList{}
	hcoList.SetGroupVersionKind(pkgcontext.HCOGVK)
	hcoList.SetAPIVersion("hco.kubevirt.io/v1beta1")

	if err := s.client.List(ctx, hcoList); err != nil {
		return nil, fmt.Errorf("failed to list HCO: %w", err)
	}

	if len(hcoList.Items) == 0 {
		return nil, fmt.Errorf("no HyperConverged resources found")
	}

	hco := &hcoList.Items[0]

	// Build render context
	renderCtx := pkgcontext.NewRenderContext(hco)

	return renderCtx, nil
}

// checkConditions evaluates if an asset's conditions are met
func (s *Server) checkConditions(assetMeta *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) bool {
	// If no conditions, always include
	if len(assetMeta.Conditions) == 0 {
		return true
	}

	// All conditions must be met (AND logic)
	for _, condition := range assetMeta.Conditions {
		switch condition.Type {
		case assets.ConditionTypeAnnotation:
			annotations := renderCtx.HCO.GetAnnotations()
			if annotations[condition.Key] != condition.Value {
				return false
			}
		case assets.ConditionTypeFeatureGate:
			// Simplified: check if feature gate is in annotations
			featureGates := renderCtx.HCO.GetAnnotations()["platform.kubevirt.io/feature-gates"]
			if !strings.Contains(featureGates, condition.Value) {
				return false
			}
		case assets.ConditionTypeHardwareDetection:
			// Hardware detection would require node inspection - skip for debug
			// In real controller, this uses hardware detectors
			return false
		}
	}

	return true
}

// getConditionDetails returns details about why conditions weren't met
func (s *Server) getConditionDetails(assetMeta *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) map[string]string {
	details := make(map[string]string)

	for _, condition := range assetMeta.Conditions {
		switch condition.Type {
		case assets.ConditionTypeAnnotation:
			annotations := renderCtx.HCO.GetAnnotations()
			actual := annotations[condition.Key]
			details[condition.Key] = fmt.Sprintf("expected=%s, actual=%s", condition.Value, actual)
		case assets.ConditionTypeFeatureGate:
			featureGates := renderCtx.HCO.GetAnnotations()["platform.kubevirt.io/feature-gates"]
			details["feature-gates"] = featureGates
			details["required"] = condition.Value
		case assets.ConditionTypeHardwareDetection:
			details["detector"] = condition.Detector
			details["status"] = "not checked (requires node access)"
		}
	}

	return details
}

// writeResponse writes the response in the requested format
func (s *Server) writeResponse(w http.ResponseWriter, data interface{}, format string) {
	var contentType string
	var output []byte
	var err error

	switch format {
	case "json":
		contentType = "application/json"
		output, err = json.MarshalIndent(data, "", "  ")
	case "yaml":
		contentType = "application/x-yaml"
		output, err = yaml.Marshal(data)
	default:
		http.Error(w, fmt.Sprintf("Unsupported format: %s", format), http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}
