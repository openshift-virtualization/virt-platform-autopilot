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

package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	embeddedassets "github.com/kubevirt/virt-platform-operator/assets"
)

const (
	// MaxYAMLSize is the maximum size of YAML content to prevent DoS attacks
	// This protects against YAML bombs and excessively large manifests
	MaxYAMLSize = 10 * 1024 * 1024 // 10MB

	// MaxYAMLDepth is the maximum nesting depth for YAML structures
	// This protects against deeply nested structures that cause stack overflow
	MaxYAMLDepth = 100
)

// Loader handles loading and parsing assets from embedded filesystem
type Loader struct {
	fs embed.FS
}

// NewLoader creates a new asset loader
func NewLoader() *Loader {
	return &Loader{
		fs: embeddedassets.EmbeddedFS,
	}
}

// LoadAsset loads a single asset by path and returns its raw content
func (l *Loader) LoadAsset(path string) ([]byte, error) {
	data, err := l.fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read asset %s: %w", path, err)
	}

	return data, nil
}

// LoadAssetAsUnstructured loads an asset and parses it as an unstructured object
// This is for non-template assets (raw YAML)
func (l *Loader) LoadAssetAsUnstructured(path string) (*unstructured.Unstructured, error) {
	data, err := l.LoadAsset(path)
	if err != nil {
		return nil, err
	}

	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, obj); err != nil {
		return nil, fmt.Errorf("failed to parse asset %s as YAML: %w", path, err)
	}

	return obj, nil
}

// LoadAssetTemplate loads a template asset (for rendering with context)
// Returns raw template content to be processed by the renderer
func (l *Loader) LoadAssetTemplate(path string) (string, error) {
	data, err := l.LoadAsset(path)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// ListAssets lists all asset files matching a glob pattern
// Pattern is relative to assets directory (e.g., "machine-config/*.yaml")
func (l *Loader) ListAssets(pattern string) ([]string, error) {
	var matches []string

	// Walk the embedded filesystem
	err := fs.WalkDir(l.fs, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		// Match against pattern
		matched, err := filepath.Match(pattern, path)
		if err != nil {
			return fmt.Errorf("invalid pattern %s: %w", pattern, err)
		}

		if matched {
			matches = append(matches, path)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list assets with pattern %s: %w", pattern, err)
	}

	return matches, nil
}

// IsTemplate returns true if the asset path appears to be a template file
func IsTemplate(path string) bool {
	return strings.HasSuffix(path, ".tpl") || strings.HasSuffix(path, ".tmpl")
}

// ParseYAML parses YAML content into an unstructured object
// Validates size and depth to prevent DoS attacks
func ParseYAML(data []byte) (*unstructured.Unstructured, error) {
	// Validate size to prevent DoS via large YAML documents
	if len(data) > MaxYAMLSize {
		return nil, fmt.Errorf("YAML content exceeds maximum size of %d bytes (got %d bytes)", MaxYAMLSize, len(data))
	}

	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, obj); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate depth to prevent DoS via deeply nested structures
	if depth := calculateDepth(obj.Object); depth > MaxYAMLDepth {
		return nil, fmt.Errorf("YAML structure exceeds maximum nesting depth of %d (got %d)", MaxYAMLDepth, depth)
	}

	return obj, nil
}

// calculateDepth recursively calculates the maximum nesting depth of a map structure
func calculateDepth(obj interface{}) int {
	switch v := obj.(type) {
	case map[string]interface{}:
		maxDepth := 0
		for _, value := range v {
			depth := calculateDepth(value)
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		return 1 + maxDepth
	case []interface{}:
		maxDepth := 0
		for _, item := range v {
			depth := calculateDepth(item)
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		return 1 + maxDepth
	default:
		return 1
	}
}

// ParseMultiYAML parses YAML content that may contain multiple documents
// Returns a slice of unstructured objects
func ParseMultiYAML(data []byte) ([]*unstructured.Unstructured, error) {
	// Validate total size to prevent DoS
	if len(data) > MaxYAMLSize {
		return nil, fmt.Errorf("YAML content exceeds maximum size of %d bytes (got %d bytes)", MaxYAMLSize, len(data))
	}

	// Split by document separator
	docs := strings.Split(string(data), "\n---\n")

	var objects []*unstructured.Unstructured
	for i, doc := range docs {
		// Skip empty documents
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		obj, err := ParseYAML([]byte(doc))
		if err != nil {
			return nil, fmt.Errorf("failed to parse document %d: %w", i, err)
		}

		objects = append(objects, obj)
	}

	return objects, nil
}
