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
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func TestCRDIntrospectionFunctions(t *testing.T) {
	// Create a fake KubeDescheduler CRD with profile enums
	crd := createTestKubeDeschedulerCRD([]string{
		"LongLifecycle",
		"DevKubeVirtRelieveAndMigrate",
		"KubeVirtRelieveAndMigrate",
	})

	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(crd).
		Build()

	loader := assets.NewLoader()
	renderer := NewRenderer(loader)
	renderer.SetClient(fakeClient)

	t.Run("crdEnum extracts all enum values", func(t *testing.T) {
		funcMap := renderer.customFuncMap()
		crdEnumFunc := funcMap["crdEnum"].(func(string, string) []string)

		enums := crdEnumFunc("kubedeschedulers.operator.openshift.io", "spec.profiles")

		if len(enums) != 3 {
			t.Errorf("Expected 3 enum values, got %d", len(enums))
		}

		expected := map[string]bool{
			"LongLifecycle":                true,
			"DevKubeVirtRelieveAndMigrate": true,
			"KubeVirtRelieveAndMigrate":    true,
		}

		for _, enum := range enums {
			if !expected[enum] {
				t.Errorf("Unexpected enum value: %s", enum)
			}
		}
	})

	t.Run("crdHasEnum returns true for existing value", func(t *testing.T) {
		funcMap := renderer.customFuncMap()
		crdHasEnumFunc := funcMap["crdHasEnum"].(func(string, string, string) bool)

		if !crdHasEnumFunc("kubedeschedulers.operator.openshift.io", "spec.profiles", "KubeVirtRelieveAndMigrate") {
			t.Error("Expected KubeVirtRelieveAndMigrate to be in enum")
		}

		if !crdHasEnumFunc("kubedeschedulers.operator.openshift.io", "spec.profiles", "DevKubeVirtRelieveAndMigrate") {
			t.Error("Expected DevKubeVirtRelieveAndMigrate to be in enum")
		}

		if !crdHasEnumFunc("kubedeschedulers.operator.openshift.io", "spec.profiles", "LongLifecycle") {
			t.Error("Expected LongLifecycle to be in enum")
		}
	})

	t.Run("crdHasEnum returns false for non-existing value", func(t *testing.T) {
		funcMap := renderer.customFuncMap()
		crdHasEnumFunc := funcMap["crdHasEnum"].(func(string, string, string) bool)

		if crdHasEnumFunc("kubedeschedulers.operator.openshift.io", "spec.profiles", "NonExistentProfile") {
			t.Error("Expected NonExistentProfile to not be in enum")
		}
	})

	t.Run("renders descheduler template with profile selection", func(t *testing.T) {
		template := `{{- $crdName := "kubedeschedulers.operator.openshift.io" -}}
{{- $profilesPath := "spec.profiles" -}}
{{- if crdHasEnum $crdName $profilesPath "KubeVirtRelieveAndMigrate" -}}
profile: KubeVirtRelieveAndMigrate
{{- else if crdHasEnum $crdName $profilesPath "DevKubeVirtRelieveAndMigrate" -}}
profile: DevKubeVirtRelieveAndMigrate
{{- else if crdHasEnum $crdName $profilesPath "LongLifecycle" -}}
profile: LongLifecycle
needsCustomization: true
{{- end -}}`

		ctx := &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"),
		}

		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("Failed to render template: %v", err)
		}

		result := string(rendered)
		expected := "profile: KubeVirtRelieveAndMigrate"
		if result != expected {
			t.Errorf("Expected profile to be '%s', got: '%s'", expected, result)
		}
	})

	t.Run("fallback to older profile when newest not available", func(t *testing.T) {
		// Create CRD with only older profiles
		oldCRD := createTestKubeDeschedulerCRD([]string{
			"LongLifecycle",
			"DevKubeVirtRelieveAndMigrate",
		})

		oldClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(oldCRD).
			Build()

		oldRenderer := NewRenderer(loader)
		oldRenderer.SetClient(oldClient)

		template := `{{- $crdName := "kubedeschedulers.operator.openshift.io" -}}
{{- $profilesPath := "spec.profiles" -}}
{{- if crdHasEnum $crdName $profilesPath "KubeVirtRelieveAndMigrate" -}}
profile: KubeVirtRelieveAndMigrate
{{- else if crdHasEnum $crdName $profilesPath "DevKubeVirtRelieveAndMigrate" -}}
profile: DevKubeVirtRelieveAndMigrate
{{- else if crdHasEnum $crdName $profilesPath "LongLifecycle" -}}
profile: LongLifecycle
needsCustomization: true
{{- end -}}`

		ctx := &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"),
		}

		rendered, err := oldRenderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("Failed to render template: %v", err)
		}

		result := string(rendered)
		expected := "profile: DevKubeVirtRelieveAndMigrate"
		if result != expected {
			t.Errorf("Expected profile to be '%s', got: '%s'", expected, result)
		}
	})
}

// createTestKubeDeschedulerCRD creates a fake KubeDescheduler CRD for testing
func createTestKubeDeschedulerCRD(profiles []string) *apiextensionsv1.CustomResourceDefinition {
	enumValues := make([]apiextensionsv1.JSON, len(profiles))
	for i, profile := range profiles {
		enumValues[i] = apiextensionsv1.JSON{Raw: []byte(`"` + profile + `"`)}
	}

	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubedeschedulers.operator.openshift.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"profiles": {
											Type: "array",
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "string",
													Enum: enumValues,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
