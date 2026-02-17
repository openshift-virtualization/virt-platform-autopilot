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
	"context"
	"fmt"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

// Renderer handles template rendering with RenderContext
type Renderer struct {
	loader *assets.Loader
	client client.Reader // Optional: for CRD introspection and object queries
}

// NewRenderer creates a new template renderer
func NewRenderer(loader *assets.Loader) *Renderer {
	return &Renderer{
		loader: loader,
	}
}

// SetClient sets the Kubernetes client for CRD introspection and object queries
func (r *Renderer) SetClient(c client.Reader) {
	r.client = c
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
	// Create template with safe functions only (not all of Sprig)
	tmpl, err := template.New(name).
		Funcs(safeFuncMap()).
		Funcs(r.customFuncMap()).
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

// safeFuncMap returns a allowlist of safe Sprig functions
// Explicitly excludes dangerous functions like:
// - env/expandenv (leak secrets)
// - genPrivateKey/genCertificate (CPU intensive, DoS risk)
// - now/date (non-deterministic, causes drift)
// - randAlpha/uuid (non-deterministic)
func safeFuncMap() template.FuncMap {
	// Get all Sprig functions
	allFuncs := sprig.TxtFuncMap()

	// Define safe function names to include
	safeFuncs := template.FuncMap{}

	// String functions (safe)
	safeNames := []string{
		// String manipulation
		"upper", "lower", "title", "untitle", "repeat", "substr",
		"nospace", "trim", "trimAll", "trimSuffix", "trimPrefix",
		"replace", "plural", "snakecase", "camelcase", "kebabcase",
		"contains", "hasPrefix", "hasSuffix", "quote", "squote",
		"cat", "indent", "nindent", "wrap", "wrapWith",

		// Logic and flow control
		"default", "empty", "coalesce", "ternary",
		"eq", "ne", "lt", "le", "gt", "ge",
		"not", "and", "or",

		// Type conversion
		"toString", "toStrings", "toInt", "toInt64", "toFloat64",
		"toBool", "toJson", "toPrettyJson", "toRawJson", "fromJson",
		"toYaml", "fromYaml",

		// Lists and collections
		"list", "append", "prepend", "first", "rest", "last",
		"initial", "reverse", "uniq", "without", "has", "compact",
		"slice", "concat", "chunk", "splitList", "join",

		// Dictionaries
		"dict", "set", "unset", "hasKey", "pluck", "merge",
		"mergeOverwrite", "keys", "pick", "omit", "values",

		// Math operations
		"add", "add1", "sub", "div", "mod", "mul", "max", "min",
		"floor", "ceil", "round",

		// Encoding (safe read-only operations)
		"b64enc", "b64dec", "b32enc", "b32dec",
	}

	// Copy only allowlisted functions
	for _, name := range safeNames {
		if fn, exists := allFuncs[name]; exists {
			safeFuncs[name] = fn
		}
	}

	return safeFuncs
}

// customFuncMap provides custom template functions beyond sprig
func (r *Renderer) customFuncMap() template.FuncMap {
	return template.FuncMap{
		// dig provides safe nested field access with default value
		// Usage: {{ dig "spec" "field" "default" .HCO }}
		"dig": dig,

		// has checks if a slice contains a value
		// Usage: {{ has "value" .HCO.spec.featureGates }}
		"has": has,

		// crdEnum extracts enum values from a CRD field
		// Usage: {{ crdEnum "kubedeschedulers.operator.openshift.io" "spec.profiles" }}
		"crdEnum": r.crdEnumFunc(),

		// crdHasEnum checks if a CRD enum contains a specific value
		// Usage: {{ crdHasEnum "kubedeschedulers.operator.openshift.io" "spec.profiles" "KubeVirtRelieveAndMigrate" }}
		"crdHasEnum": r.crdHasEnumFunc(),

		// objectExists checks if a Kubernetes object exists
		// Usage: {{ objectExists "PrometheusRule" "openshift-kube-descheduler-operator" "descheduler-rules" }}
		"objectExists": r.objectExistsFunc(),

		// prometheusRuleHasRecordingRule checks if a PrometheusRule contains a specific recording rule
		// Usage: {{ prometheusRuleHasRecordingRule "openshift-kube-descheduler-operator" "descheduler-rules" "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m" }}
		"prometheusRuleHasRecordingRule": r.prometheusRuleHasRecordingRuleFunc(),
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

// crdEnumFunc returns a function that extracts enum values from a CRD field
func (r *Renderer) crdEnumFunc() func(string, string) []string {
	return func(crdName, fieldPath string) []string {
		if r.client == nil {
			return []string{}
		}

		// Fetch the CRD
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := r.client.Get(context.Background(), types.NamespacedName{Name: crdName}, crd); err != nil {
			return []string{}
		}

		// Extract enum values from the field path
		enums, err := extractCRDEnum(crd, fieldPath)
		if err != nil {
			return []string{}
		}

		return enums
	}
}

// crdHasEnumFunc returns a function that checks if a CRD enum contains a value
func (r *Renderer) crdHasEnumFunc() func(string, string, string) bool {
	return func(crdName, fieldPath, value string) bool {
		if r.client == nil {
			return false
		}

		// Fetch the CRD
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := r.client.Get(context.Background(), types.NamespacedName{Name: crdName}, crd); err != nil {
			return false
		}

		// Extract enum values from the field path
		enums, err := extractCRDEnum(crd, fieldPath)
		if err != nil {
			return false
		}

		// Check if value is in enums
		for _, enum := range enums {
			if enum == value {
				return true
			}
		}

		return false
	}
}

// extractCRDEnum extracts enum values from a CRD field path
// fieldPath examples: "spec.profiles", "spec.mode"
func extractCRDEnum(crd *apiextensionsv1.CustomResourceDefinition, fieldPath string) ([]string, error) {
	// Get the storage version's schema
	var schema *apiextensionsv1.CustomResourceValidation
	for _, version := range crd.Spec.Versions {
		if version.Storage {
			schema = version.Schema
			break
		}
	}

	if schema == nil || schema.OpenAPIV3Schema == nil {
		return nil, fmt.Errorf("no OpenAPI schema found in CRD")
	}

	// Navigate the field path
	current := schema.OpenAPIV3Schema
	parts := splitFieldPath(fieldPath)

	for _, part := range parts {
		if current.Properties == nil {
			return nil, fmt.Errorf("no properties found at path %s", part)
		}

		next, ok := current.Properties[part]
		if !ok {
			return nil, fmt.Errorf("field %s not found in schema", part)
		}

		current = &next
	}

	// Check if this field is an array with enum items
	if current.Type == "array" && current.Items != nil && current.Items.Schema != nil {
		return extractEnumValues(current.Items.Schema), nil
	}

	// Check if this field directly has enum values
	return extractEnumValues(current), nil
}

// extractEnumValues extracts string values from JSON enum
func extractEnumValues(schema *apiextensionsv1.JSONSchemaProps) []string {
	if len(schema.Enum) == 0 {
		return []string{}
	}

	var values []string
	for _, enum := range schema.Enum {
		// enum.Raw is []byte containing JSON representation
		// For strings, it's: "value" (with quotes)
		raw := string(enum.Raw)
		// Remove surrounding quotes if present
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			values = append(values, raw[1:len(raw)-1])
		} else {
			values = append(values, raw)
		}
	}

	return values
}

// splitFieldPath splits a field path like "spec.profiles" into ["spec", "profiles"]
func splitFieldPath(path string) []string {
	var parts []string
	current := ""
	for _, ch := range path {
		if ch == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// objectExistsFunc returns a function that checks if a Kubernetes object exists
func (r *Renderer) objectExistsFunc() func(string, string, string) bool {
	return func(kind, namespace, name string) bool {
		if r.client == nil {
			return false
		}

		obj := &unstructured.Unstructured{}
		obj.SetKind(kind)
		obj.SetAPIVersion("monitoring.coreos.com/v1") // Default for PrometheusRule

		err := r.client.Get(context.Background(), types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, obj)

		return err == nil
	}
}

// prometheusRuleHasRecordingRuleFunc returns a function that checks if a PrometheusRule contains a recording rule
func (r *Renderer) prometheusRuleHasRecordingRuleFunc() func(string, string, string) bool {
	return func(namespace, name, recordName string) bool {
		if r.client == nil {
			return false
		}

		// Fetch the PrometheusRule
		obj := &unstructured.Unstructured{}
		obj.SetKind("PrometheusRule")
		obj.SetAPIVersion("monitoring.coreos.com/v1")

		err := r.client.Get(context.Background(), types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, obj)

		if err != nil {
			return false
		}

		// Navigate to spec.groups[].rules[]
		spec, ok := obj.Object["spec"].(map[string]interface{})
		if !ok {
			return false
		}

		groups, ok := spec["groups"].([]interface{})
		if !ok {
			return false
		}

		// Search through all groups and rules
		for _, group := range groups {
			groupMap, ok := group.(map[string]interface{})
			if !ok {
				continue
			}

			rules, ok := groupMap["rules"].([]interface{})
			if !ok {
				continue
			}

			for _, rule := range rules {
				ruleMap, ok := rule.(map[string]interface{})
				if !ok {
					continue
				}

				// Check if this is a recording rule with the matching name
				if record, ok := ruleMap["record"].(string); ok && record == recordName {
					return true
				}
			}
		}

		return false
	}
}
