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
	"bytes"
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
	pkgrender "github.com/kubevirt/virt-platform-autopilot/pkg/render"
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

// handleRender renders all assets and returns them
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
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
	showExcluded := r.URL.Query().Get("show-excluded") == "true"

	renderCtx, err := s.getRenderContext(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get render context: %v", err), http.StatusInternalServerError)
		return
	}

	outputs := pkgrender.BuildOutputs(s.registry.ListAssetsByReconcileOrder(), s.renderer, renderCtx, showExcluded)
	s.writeRenderResponse(w, outputs, format)
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

	assetMeta, err := s.registry.GetAsset(assetName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Asset not found: %v", err), http.StatusNotFound)
		return
	}

	renderCtx, err := s.getRenderContext(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get render context: %v", err), http.StatusInternalServerError)
		return
	}

	output := pkgrender.RenderOutput{
		Asset:      assetMeta.Name,
		Path:       assetMeta.Path,
		Component:  assetMeta.Component,
		Conditions: assetMeta.Conditions,
	}

	if !pkgrender.CheckConditions(assetMeta, renderCtx) {
		output.Status = "EXCLUDED"
		output.Reason = "Conditions not met"
		s.writeRenderResponse(w, []pkgrender.RenderOutput{output}, format)
		return
	}

	rendered, err := s.renderer.RenderAsset(assetMeta, renderCtx)
	if err != nil {
		output.Status = "ERROR"
		output.Reason = err.Error()
		s.writeRenderResponse(w, []pkgrender.RenderOutput{output}, format)
		return
	}

	if rendered == nil {
		output.Status = "EXCLUDED"
		output.Reason = "Conditional template rendered empty"
		s.writeRenderResponse(w, []pkgrender.RenderOutput{output}, format)
		return
	}

	// Check root exclusion; fail-open if the annotation cannot be parsed.
	disabledAnnotation := renderCtx.HCO.GetAnnotations()[engine.DisabledResourcesAnnotation]
	if disabledAnnotation != "" {
		rules, err := engine.ParseDisabledResources(disabledAnnotation)
		if err == nil && engine.IsResourceExcluded(rendered.GetKind(), rendered.GetNamespace(), rendered.GetName(), rules) {
			output.Status = "FILTERED"
			output.Reason = "Root exclusion (disabled-resources annotation)"
			s.writeRenderResponse(w, []pkgrender.RenderOutput{output}, format)
			return
		}
	}

	output.Status = "INCLUDED"
	output.Object = rendered
	s.writeRenderResponse(w, []pkgrender.RenderOutput{output}, format)
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

	renderCtx, err := s.getRenderContext(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get render context: %v", err), http.StatusInternalServerError)
		return
	}

	exclusions := []ExclusionInfo{}
	assetList := s.registry.ListAssetsByReconcileOrder()

	for _, assetMeta := range assetList {
		if !pkgrender.CheckConditions(&assetMeta, renderCtx) {
			exclusions = append(exclusions, ExclusionInfo{
				Asset:     assetMeta.Name,
				Path:      assetMeta.Path,
				Component: assetMeta.Component,
				Reason:    "Conditions not met",
				Details:   s.getConditionDetails(&assetMeta, renderCtx),
				Metadata:  &assetMeta,
			})
			continue
		}

		rendered, err := s.renderer.RenderAsset(&assetMeta, renderCtx)
		if err != nil || rendered == nil {
			reason := "Template rendered empty"
			if err != nil {
				reason = fmt.Sprintf("Render error: %v", err)
			}
			exclusions = append(exclusions, ExclusionInfo{
				Asset:     assetMeta.Name,
				Path:      assetMeta.Path,
				Component: assetMeta.Component,
				Reason:    reason,
				Metadata:  &assetMeta,
			})
			continue
		}

		disabledAnnotation := renderCtx.HCO.GetAnnotations()[engine.DisabledResourcesAnnotation]
		if disabledAnnotation != "" {
			rules, err := engine.ParseDisabledResources(disabledAnnotation)
			if err != nil {
				continue
			}
			if engine.IsResourceExcluded(rendered.GetKind(), rendered.GetNamespace(), rendered.GetName(), rules) {
				exclusions = append(exclusions, ExclusionInfo{
					Asset:     assetMeta.Name,
					Path:      assetMeta.Path,
					Component: assetMeta.Component,
					Reason:    "Root exclusion",
					Details: map[string]string{
						"annotation": engine.DisabledResourcesAnnotation,
						"value":      disabledAnnotation,
						"resource":   fmt.Sprintf("%s/%s/%s", rendered.GetKind(), rendered.GetNamespace(), rendered.GetName()),
					},
					Metadata: &assetMeta,
				})
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

	tombstones, err := s.loader.LoadTombstones()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load tombstones: %v", err), http.StatusInternalServerError)
		return
	}

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
	hcoList := &unstructured.UnstructuredList{}
	hcoList.SetGroupVersionKind(pkgcontext.HCOGVK)
	hcoList.SetAPIVersion("hco.kubevirt.io/v1beta1")

	if err := s.client.List(ctx, hcoList); err != nil {
		return nil, fmt.Errorf("failed to list HCO: %w", err)
	}

	if len(hcoList.Items) == 0 {
		return nil, fmt.Errorf("no HyperConverged resources found")
	}

	return pkgcontext.NewRenderContext(&hcoList.Items[0]), nil
}

// getConditionDetails returns details about why conditions weren't met
func (s *Server) getConditionDetails(assetMeta *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) map[string]string {
	details := make(map[string]string)

	for _, condition := range assetMeta.Conditions {
		switch condition.Type {
		case assets.ConditionTypeAnnotation:
			actual := renderCtx.HCO.GetAnnotations()[condition.Key]
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

// writeRenderResponse writes RenderOutput items in the requested format.
// YAML output is multi-document with comment headers (directly usable with
// kubectl apply), matching the render CLI subcommand.
func (s *Server) writeRenderResponse(w http.ResponseWriter, outputs []pkgrender.RenderOutput, format string) {
	switch format {
	case "json":
		data, err := json.MarshalIndent(outputs, "", "  ")
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to marshal response: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case "yaml":
		var buf bytes.Buffer
		if err := pkgrender.WriteYAML(&buf, outputs); err != nil {
			http.Error(w, fmt.Sprintf("Failed to marshal response: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	default:
		http.Error(w, fmt.Sprintf("Unsupported format: %s", format), http.StatusBadRequest)
	}
}

// writeResponse writes the response in the requested format (used for
// non-render endpoints such as /debug/exclusions and /debug/tombstones).
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
