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

package context

import (
	"testing"
)

func TestHardwareContext_AsMap(t *testing.T) {
	tests := []struct {
		name string
		hw   *HardwareContext
		want map[string]bool
	}{
		{
			name: "all hardware present",
			hw: &HardwareContext{
				PCIDevicesPresent: true,
				NUMANodesPresent:  true,
				VFIOCapable:       true,
				USBDevicesPresent: true,
				GPUPresent:        true,
			},
			want: map[string]bool{
				"pciDevicesPresent": true,
				"numaNodesPresent":  true,
				"vfioCapable":       true,
				"usbDevicesPresent": true,
				"gpuPresent":        true,
			},
		},
		{
			name: "no hardware present",
			hw: &HardwareContext{
				PCIDevicesPresent: false,
				NUMANodesPresent:  false,
				VFIOCapable:       false,
				USBDevicesPresent: false,
				GPUPresent:        false,
			},
			want: map[string]bool{
				"pciDevicesPresent": false,
				"numaNodesPresent":  false,
				"vfioCapable":       false,
				"usbDevicesPresent": false,
				"gpuPresent":        false,
			},
		},
		{
			name: "mixed hardware availability",
			hw: &HardwareContext{
				PCIDevicesPresent: true,
				NUMANodesPresent:  false,
				VFIOCapable:       true,
				USBDevicesPresent: false,
				GPUPresent:        true,
			},
			want: map[string]bool{
				"pciDevicesPresent": true,
				"numaNodesPresent":  false,
				"vfioCapable":       true,
				"usbDevicesPresent": false,
				"gpuPresent":        true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.hw.AsMap()

			if len(got) != len(tt.want) {
				t.Errorf("AsMap() returned map with %d keys, want %d", len(got), len(tt.want))
			}

			for key, wantVal := range tt.want {
				gotVal, exists := got[key]
				if !exists {
					t.Errorf("AsMap() missing key %q", key)
					continue
				}
				if gotVal != wantVal {
					t.Errorf("AsMap()[%q] = %v, want %v", key, gotVal, wantVal)
				}
			}
		})
	}
}
