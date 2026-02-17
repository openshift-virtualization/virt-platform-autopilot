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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func TestPrometheusRuleIntrospection(t *testing.T) {
	scheme := runtime.NewScheme()

	t.Run("objectExists returns true when object exists", func(t *testing.T) {
		// Create a fake PrometheusRule
		prometheusRule := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "monitoring.coreos.com/v1",
				"kind":       "PrometheusRule",
				"metadata": map[string]interface{}{
					"name":      "descheduler-rules",
					"namespace": "openshift-kube-descheduler-operator",
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(prometheusRule).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		funcMap := renderer.customFuncMap()
		objectExistsFunc := funcMap["objectExists"].(func(string, string, string) bool)

		exists := objectExistsFunc("PrometheusRule", "openshift-kube-descheduler-operator", "descheduler-rules")
		if !exists {
			t.Error("Expected PrometheusRule to exist")
		}
	})

	t.Run("objectExists returns false when object does not exist", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		funcMap := renderer.customFuncMap()
		objectExistsFunc := funcMap["objectExists"].(func(string, string, string) bool)

		exists := objectExistsFunc("PrometheusRule", "openshift-kube-descheduler-operator", "descheduler-rules")
		if exists {
			t.Error("Expected PrometheusRule to not exist")
		}
	})

	t.Run("prometheusRuleHasRecordingRule returns true when rule exists", func(t *testing.T) {
		// Create a PrometheusRule with the specific recording rule
		prometheusRule := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "monitoring.coreos.com/v1",
				"kind":       "PrometheusRule",
				"metadata": map[string]interface{}{
					"name":      "descheduler-rules",
					"namespace": "openshift-kube-descheduler-operator",
				},
				"spec": map[string]interface{}{
					"groups": []interface{}{
						map[string]interface{}{
							"name": "descheduler",
							"rules": []interface{}{
								map[string]interface{}{
									"record": "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m",
									"expr":   "some_query",
								},
								map[string]interface{}{
									"record": "some_other_rule",
									"expr":   "some_other_query",
								},
							},
						},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(prometheusRule).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		funcMap := renderer.customFuncMap()
		hasRuleFunc := funcMap["prometheusRuleHasRecordingRule"].(func(string, string, string) bool)

		hasRule := hasRuleFunc("openshift-kube-descheduler-operator", "descheduler-rules", "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m")
		if !hasRule {
			t.Error("Expected recording rule to be found")
		}
	})

	t.Run("prometheusRuleHasRecordingRule returns false when rule does not exist", func(t *testing.T) {
		// Create a PrometheusRule without the specific rule
		prometheusRule := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "monitoring.coreos.com/v1",
				"kind":       "PrometheusRule",
				"metadata": map[string]interface{}{
					"name":      "descheduler-rules",
					"namespace": "openshift-kube-descheduler-operator",
				},
				"spec": map[string]interface{}{
					"groups": []interface{}{
						map[string]interface{}{
							"name": "descheduler",
							"rules": []interface{}{
								map[string]interface{}{
									"record": "some_other_rule",
									"expr":   "some_query",
								},
							},
						},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(prometheusRule).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		funcMap := renderer.customFuncMap()
		hasRuleFunc := funcMap["prometheusRuleHasRecordingRule"].(func(string, string, string) bool)

		hasRule := hasRuleFunc("openshift-kube-descheduler-operator", "descheduler-rules", "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m")
		if hasRule {
			t.Error("Expected recording rule to not be found")
		}
	})

	t.Run("template renders with devActualUtilizationProfile based on PrometheusRule", func(t *testing.T) {
		// Create a PrometheusRule with the k3 rule
		prometheusRule := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "monitoring.coreos.com/v1",
				"kind":       "PrometheusRule",
				"metadata": map[string]interface{}{
					"name":      "descheduler-rules",
					"namespace": "openshift-kube-descheduler-operator",
				},
				"spec": map[string]interface{}{
					"groups": []interface{}{
						map[string]interface{}{
							"name": "descheduler",
							"rules": []interface{}{
								map[string]interface{}{
									"record": "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m",
									"expr":   "some_query",
								},
							},
						},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(prometheusRule).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		template := `{{- $devActualUtilizationProfile := "" -}}
{{- if prometheusRuleHasRecordingRule "openshift-kube-descheduler-operator" "descheduler-rules" "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m" -}}
  {{- $devActualUtilizationProfile = "PrometheusCPUMemoryCombinedProfile" -}}
{{- else if objectExists "PrometheusRule" "openshift-kube-descheduler-operator" "descheduler-rules" -}}
  {{- $devActualUtilizationProfile = "PrometheusCPUCombined" -}}
{{- end -}}
{{- if $devActualUtilizationProfile -}}
devActualUtilizationProfile: {{ $devActualUtilizationProfile }}
{{- end -}}`

		ctx := &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"),
		}

		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("Failed to render template: %v", err)
		}

		result := string(rendered)
		expected := "devActualUtilizationProfile: PrometheusCPUMemoryCombinedProfile"
		if result != expected {
			t.Errorf("Expected '%s', got: '%s'", expected, result)
		}
	})

	t.Run("template uses fallback profile when k3 rule not found", func(t *testing.T) {
		// Create a PrometheusRule without the k3 rule
		prometheusRule := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "monitoring.coreos.com/v1",
				"kind":       "PrometheusRule",
				"metadata": map[string]interface{}{
					"name":      "descheduler-rules",
					"namespace": "openshift-kube-descheduler-operator",
				},
				"spec": map[string]interface{}{
					"groups": []interface{}{
						map[string]interface{}{
							"name":  "descheduler",
							"rules": []interface{}{},
						},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(prometheusRule).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		template := `{{- $devActualUtilizationProfile := "" -}}
{{- if prometheusRuleHasRecordingRule "openshift-kube-descheduler-operator" "descheduler-rules" "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m" -}}
  {{- $devActualUtilizationProfile = "PrometheusCPUMemoryCombinedProfile" -}}
{{- else if objectExists "PrometheusRule" "openshift-kube-descheduler-operator" "descheduler-rules" -}}
  {{- $devActualUtilizationProfile = "PrometheusCPUCombined" -}}
{{- end -}}
{{- if $devActualUtilizationProfile -}}
devActualUtilizationProfile: {{ $devActualUtilizationProfile }}
{{- end -}}`

		ctx := &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"),
		}

		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("Failed to render template: %v", err)
		}

		result := string(rendered)
		expected := "devActualUtilizationProfile: PrometheusCPUCombined"
		if result != expected {
			t.Errorf("Expected '%s', got: '%s'", expected, result)
		}
	})
}
