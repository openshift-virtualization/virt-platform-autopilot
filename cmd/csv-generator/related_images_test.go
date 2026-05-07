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

package main

import (
	"reflect"
	"testing"

	"github.com/kubevirt/virt-platform-autopilot/cmd/csv-generator/parser"
)

func TestBuildRelatedImages(t *testing.T) {
	tests := []struct {
		name                   string
		operatorImage          string
		additionalImageEnvVars []parser.EnvVar
		expected               []RelatedImage
	}{
		{
			name:                   "operator image only",
			operatorImage:          "quay.io/test/autopilot@sha256:abc123",
			additionalImageEnvVars: nil,
			expected: []RelatedImage{
				{Image: "quay.io/test/autopilot@sha256:abc123"},
			},
		},
		{
			name:                   "operator image with empty additional images slice",
			operatorImage:          "quay.io/test/autopilot@sha256:abc123",
			additionalImageEnvVars: []parser.EnvVar{},
			expected: []RelatedImage{
				{Image: "quay.io/test/autopilot@sha256:abc123"},
			},
		},
		{
			name:          "operator image with one additional image",
			operatorImage: "quay.io/test/autopilot@sha256:abc123",
			additionalImageEnvVars: []parser.EnvVar{
				{Name: "NETWORK_RESOURCES_INJECTOR_IMAGE", Value: "registry.redhat.io/openshift4/ose-sriov-dp-admission-controller-rhel9@sha256:def456"},
			},
			expected: []RelatedImage{
				{Image: "quay.io/test/autopilot@sha256:abc123"},
				{Image: "registry.redhat.io/openshift4/ose-sriov-dp-admission-controller-rhel9@sha256:def456"},
			},
		},
		{
			name:          "operator image with multiple additional images",
			operatorImage: "quay.io/test/autopilot@sha256:abc123",
			additionalImageEnvVars: []parser.EnvVar{
				{Name: "NETWORK_RESOURCES_INJECTOR_IMAGE", Value: "registry.redhat.io/openshift4/ose-sriov-dp-admission-controller-rhel9@sha256:def456"},
				{Name: "ANOTHER_IMAGE", Value: "quay.io/test/another@sha256:xyz789"},
				{Name: "THIRD_IMAGE", Value: "registry.example.com/test/third:v1.0"},
			},
			expected: []RelatedImage{
				{Image: "quay.io/test/autopilot@sha256:abc123"},
				{Image: "registry.redhat.io/openshift4/ose-sriov-dp-admission-controller-rhel9@sha256:def456"},
				{Image: "quay.io/test/another@sha256:xyz789"},
				{Image: "registry.example.com/test/third:v1.0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildRelatedImages(tt.operatorImage, tt.additionalImageEnvVars)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("buildRelatedImages() = %+v, expected %+v", result, tt.expected)
			}
		})
	}
}
