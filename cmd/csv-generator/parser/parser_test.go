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

package parser

import (
	"reflect"
	"testing"
)

func TestParseAdditionalImages(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []EnvVar
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:  "single image",
			input: "NETWORK_RESOURCES_INJECTOR_IMAGE:registry.redhat.io/cnv/nri:v1.0",
			expected: []EnvVar{
				{Name: "NETWORK_RESOURCES_INJECTOR_IMAGE", Value: "registry.redhat.io/cnv/nri:v1.0"},
			},
		},
		{
			name:  "multiple images",
			input: "NETWORK_RESOURCES_INJECTOR_IMAGE:registry.redhat.io/cnv/nri:v1.0,FOO_IMAGE:registry.redhat.io/cnv/foo:latest",
			expected: []EnvVar{
				{Name: "NETWORK_RESOURCES_INJECTOR_IMAGE", Value: "registry.redhat.io/cnv/nri:v1.0"},
				{Name: "FOO_IMAGE", Value: "registry.redhat.io/cnv/foo:latest"},
			},
		},
		{
			name:  "with whitespace",
			input: " NETWORK_RESOURCES_INJECTOR_IMAGE : registry.redhat.io/cnv/nri:v1.0 , FOO_IMAGE : registry.redhat.io/cnv/foo:latest ",
			expected: []EnvVar{
				{Name: "NETWORK_RESOURCES_INJECTOR_IMAGE", Value: "registry.redhat.io/cnv/nri:v1.0"},
				{Name: "FOO_IMAGE", Value: "registry.redhat.io/cnv/foo:latest"},
			},
		},
		{
			name:     "malformed - missing colon",
			input:    "NETWORK_RESOURCES_INJECTOR_IMAGE",
			expected: nil,
		},
		{
			name:     "malformed - empty key",
			input:    ":registry.redhat.io/cnv/nri:v1.0",
			expected: nil,
		},
		{
			name:     "malformed - empty value",
			input:    "NETWORK_RESOURCES_INJECTOR_IMAGE:",
			expected: nil,
		},
		{
			name:  "mixed valid and invalid",
			input: "VALID_IMAGE:registry.redhat.io/cnv/image:v1.0,MALFORMED,ANOTHER_VALID:registry.redhat.io/cnv/other:v2.0",
			expected: []EnvVar{
				{Name: "VALID_IMAGE", Value: "registry.redhat.io/cnv/image:v1.0"},
				{Name: "ANOTHER_VALID", Value: "registry.redhat.io/cnv/other:v2.0"},
			},
		},
		{
			name:  "image with multiple colons",
			input: "MY_IMAGE:registry.redhat.io/cnv/image:v1.0:extra",
			expected: []EnvVar{
				{Name: "MY_IMAGE", Value: "registry.redhat.io/cnv/image:v1.0:extra"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseAdditionalImages(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ParseAdditionalImages(%q) = %+v, expected %+v", tt.input, result, tt.expected)
			}
		})
	}
}
