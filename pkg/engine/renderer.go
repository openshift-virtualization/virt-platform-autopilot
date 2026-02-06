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

package engine

import (
	"bytes"
	"fmt"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-operator/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-operator/pkg/context"
)

// Renderer handles template rendering with RenderContext
type Renderer struct {
	loader *assets.Loader
}

// NewRenderer creates a new template renderer
func NewRenderer(loader *assets.Loader) *Renderer {
	return &Renderer{
		loader: loader,
	}
}

// RenderAsset renders an asset template with the given context
// Returns nil if template conditions evaluate to empty (e.g., hardware not present)
func (r *Renderer) RenderAsset(assetMeta *assets.AssetMetadata, ctx *pkgcontext.RenderContext) (*unstructured.Unstructured, error) {
	// Check if this is a template file
	if !assets.IsTemplate(assetMeta.Path) {
		// Load as static YAML
		return r.loader.LoadAssetAsUnstructured(assetMeta.Path)
	}

	// Load template content
	templateContent, err := r.loader.LoadAssetTemplate(assetMeta.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load template %s: %w", assetMeta.Path, err)
	}

	// Render template
	rendered, err := r.renderTemplate(assetMeta.Name, templateContent, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to render template %s: %w", assetMeta.Path, err)
	}

	// Handle empty rendering (conditional templates that don't apply)
	if len(bytes.TrimSpace(rendered)) == 0 {
		return nil, nil
	}

	// Parse rendered YAML
	obj, err := assets.ParseYAML(rendered)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rendered template %s: %w", assetMeta.Path, err)
	}

	return obj, nil
}

// RenderMultiAsset renders a template that may contain multiple YAML documents
func (r *Renderer) RenderMultiAsset(assetMeta *assets.AssetMetadata, ctx *pkgcontext.RenderContext) ([]*unstructured.Unstructured, error) {
	// Check if this is a template file
	if !assets.IsTemplate(assetMeta.Path) {
		// Load as static YAML (may be multi-doc)
		data, err := r.loader.LoadAsset(assetMeta.Path)
		if err != nil {
			return nil, err
		}
		return assets.ParseMultiYAML(data)
	}

	// Load template content
	templateContent, err := r.loader.LoadAssetTemplate(assetMeta.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load template %s: %w", assetMeta.Path, err)
	}

	// Render template
	rendered, err := r.renderTemplate(assetMeta.Name, templateContent, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to render template %s: %w", assetMeta.Path, err)
	}

	// Handle empty rendering
	if len(bytes.TrimSpace(rendered)) == 0 {
		return nil, nil
	}

	// Parse rendered YAML (multi-document)
	objs, err := assets.ParseMultiYAML(rendered)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rendered template %s: %w", assetMeta.Path, err)
	}

	return objs, nil
}

// renderTemplate renders a template string with the given context
func (r *Renderer) renderTemplate(name, templateContent string, ctx *pkgcontext.RenderContext) ([]byte, error) {
	// Create template with sprig functions
	tmpl, err := template.New(name).
		Funcs(sprig.TxtFuncMap()).
		Funcs(customFuncMap()).
		Parse(templateContent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	// Render template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.Bytes(), nil
}

// customFuncMap provides custom template functions beyond sprig
func customFuncMap() template.FuncMap {
	return template.FuncMap{
		// dig provides safe nested field access with default value
		// Usage: {{ dig "spec" "field" "default" .HCO }}
		"dig": dig,

		// has checks if a slice contains a value
		// Usage: {{ has "value" .HCO.spec.featureGates }}
		"has": has,
	}
}

// dig safely accesses nested fields with a default value
// This is already provided by sprig, but we include it for clarity
func dig(keys ...interface{}) interface{} {
	if len(keys) < 2 {
		return nil
	}

	// Last argument is the object to traverse
	obj := keys[len(keys)-1]

	// Second to last is the default value
	defaultVal := keys[len(keys)-2]

	// Remaining are the path keys
	pathKeys := keys[:len(keys)-2]

	// Navigate through the path
	current := obj
	for _, key := range pathKeys {
		if m, ok := current.(map[string]interface{}); ok {
			keyStr, ok := key.(string)
			if !ok {
				return defaultVal
			}
			val, exists := m[keyStr]
			if !exists {
				return defaultVal
			}
			current = val
		} else {
			return defaultVal
		}
	}

	return current
}

// has checks if a slice contains a value
func has(needle interface{}, haystack interface{}) bool {
	switch h := haystack.(type) {
	case []interface{}:
		for _, item := range h {
			if item == needle {
				return true
			}
		}
	case []string:
		needleStr, ok := needle.(string)
		if !ok {
			return false
		}
		for _, item := range h {
			if item == needleStr {
				return true
			}
		}
	}
	return false
}
