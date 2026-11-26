/*
Copyright 2026 The Virt Platform Autopilot Authors.

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
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

const swapTemplate = `key: {{ readAsset "machine-config/01-swap/helper.conf" | b64enc }}
{{- if .Params.Enable }}
alt: {{ readAsset "machine-config/01-swap/alt.conf" | gzip | b64enc }}
{{- else }}
alt: {{ readAsset "machine-config/01-swap/helper.conf" }}
{{- end }}
{{- range .Params.Items }}
item: {{ readAsset "machine-config/01-swap/item.txt" }}
{{- end }}
nested: {{ printf "%s" (readAsset "machine-config/01-swap/nested.sh") }}
`

func supportingTestFS(extra map[string]string) fstest.MapFS {
	files := map[string]string{
		"active/machine-config/01-swap.yaml.tpl":     swapTemplate,
		"active/plain/config.yaml":                   "name: {{ dig \"a\" \"b\" \"c\" .X }}",
		"active/other/thing.yaml.tpl":                "{{- if crdHasEnum .CRD \"spec.path\" \"Value\" }}\nok\n{{- end }}",
		"active/machine-config/01-swap/helper.conf":  "helper",
		"active/machine-config/01-swap/alt.conf":     "alt",
		"active/machine-config/01-swap/item.txt":     "item",
		"active/machine-config/01-swap/nested.sh":    "nested",
		"active/machine-config/01-swap/unreferenced": "ignored",
	}
	for path, content := range extra {
		files[path] = content
	}
	mapFS := make(fstest.MapFS, len(files))
	for path, content := range files {
		mapFS[path] = &fstest.MapFile{Data: []byte(content)}
	}
	return mapFS
}

func supportingTestCatalog() *AssetCatalog {
	return &AssetCatalog{
		Assets: []AssetMetadata{
			{Name: "swap", Path: "active/machine-config/01-swap.yaml.tpl"},
			{Name: "plain", Path: "active/plain/config.yaml"},
			{Name: "other", Path: "active/other/thing.yaml.tpl"},
		},
	}
}

func TestDeriveAssetStatusesCollectsLiteralRefs(t *testing.T) {
	catalog := supportingTestCatalog()

	statuses, err := deriveAssetStatuses(catalog, supportingTestFS(nil))
	if err != nil {
		t.Fatalf("deriveAssetStatuses() error = %v", err)
	}

	want := []AssetStatus{
		{
			Name: "swap",
			SupportingFiles: []string{
				"active/machine-config/01-swap/alt.conf",
				"active/machine-config/01-swap/helper.conf",
				"active/machine-config/01-swap/item.txt",
				"active/machine-config/01-swap/nested.sh",
			},
		},
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Errorf("deriveAssetStatuses() = %+v, want %+v", statuses, want)
	}
}

func TestDeriveAssetStatusesSkipsTemplatesWithoutReadAsset(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{
			// Uses a renderer function with no stub: parsing it would fail,
			// so the readAsset pre-filter must skip it entirely.
			{Name: "other", Path: "active/other/thing.yaml.tpl"},
		},
	}

	statuses, err := deriveAssetStatuses(catalog, supportingTestFS(nil))
	if err != nil {
		t.Fatalf("deriveAssetStatuses() error = %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("deriveAssetStatuses() = %+v, want no entries", statuses)
	}
}

func TestDeriveAssetStatusesRejectsNonLiteralReadAssetArg(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{
			{Name: "swap", Path: "active/machine-config/01-swap.yaml.tpl"},
		},
	}
	files := supportingTestFS(map[string]string{
		"active/machine-config/01-swap.yaml.tpl": `{{ $p := "machine-config/01-swap/helper.conf" }}
key: {{ readAsset $p }}`,
	})

	_, err := deriveAssetStatuses(catalog, files)
	if err == nil {
		t.Fatal("deriveAssetStatuses() = nil error, want non-literal argument rejection")
	}
	if !strings.Contains(err.Error(), "literal path") {
		t.Errorf("error = %v, want non-literal argument rejection", err)
	}
}

func TestDeriveAssetStatusesRejectsMissingReferencedFile(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{
			{Name: "swap", Path: "active/machine-config/01-swap.yaml.tpl"},
		},
	}
	files := supportingTestFS(map[string]string{
		"active/machine-config/01-swap.yaml.tpl": `key: {{ readAsset "machine-config/01-swap/gone.conf" }}`,
	})

	_, err := deriveAssetStatuses(catalog, files)
	if err == nil {
		t.Fatal("deriveAssetStatuses() = nil error, want missing reference rejection")
	}
	if !strings.Contains(err.Error(), "active/machine-config/01-swap/gone.conf") {
		t.Errorf("error = %v, want the missing path named", err)
	}
}

func TestDeriveAssetStatusesRejectsUnknownTemplateFunction(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{
			{Name: "swap", Path: "active/machine-config/01-swap.yaml.tpl"},
		},
	}
	files := supportingTestFS(map[string]string{
		"active/machine-config/01-swap.yaml.tpl": `key: {{ readAsset "machine-config/01-swap/helper.conf" | crdHasEnum }}`,
	})

	_, err := deriveAssetStatuses(catalog, files)
	if err == nil {
		t.Fatal("deriveAssetStatuses() = nil error, want unknown function rejection")
	}
	if !strings.Contains(err.Error(), `function "crdHasEnum" not defined`) {
		t.Errorf("error = %v, want the unknown function named", err)
	}
}

func TestDeriveAssetStatusesRejectsMissingTemplate(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{
			{Name: "swap", Path: "active/machine-config/nowhere.yaml.tpl"},
		},
	}

	_, err := deriveAssetStatuses(catalog, supportingTestFS(nil))
	if err == nil {
		t.Fatal("deriveAssetStatuses() = nil error, want missing template rejection")
	}
	if !strings.Contains(err.Error(), "reading template of asset") {
		t.Errorf("error = %v, want the missing template named", err)
	}
}

func TestDeriveFeatureCatalogIncludesAssetStatuses(t *testing.T) {
	catalog := supportingTestCatalog()

	output, err := DeriveFeatureCatalog(catalog, supportingTestFS(nil))
	if err != nil {
		t.Fatalf("DeriveFeatureCatalog() error = %v", err)
	}

	if len(output.Assets) != 1 || output.Assets[0].Name != "swap" {
		t.Fatalf("DeriveFeatureCatalog().Assets = %+v, want a single swap entry", output.Assets)
	}
}
