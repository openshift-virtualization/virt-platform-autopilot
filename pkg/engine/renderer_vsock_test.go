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

package engine

import (
	"encoding/base64"
	"maps"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestVSOCKLocalMachineConfigRendering(t *testing.T) {
	dataURI := func(content string) string {
		return "data:text/plain;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(content))
	}
	wantFiles := map[string]string{
		"/etc/modules-load.d/90-virt-vsock.conf": dataURI("vsock\n"),
		"/etc/sysctl.d/90-virt-vsock.conf":       dataURI("net.vsock.child_ns_mode = local\n"),
	}

	for _, assetName := range []string{"vsock-local", "vsock-local-master"} {
		t.Run(assetName, func(t *testing.T) {
			obj, _, _ := renderHCOAsset(t, assetName)
			files, found, err := unstructured.NestedSlice(obj.Object, "spec", "config", "storage", "files")
			if err != nil || !found || len(files) != len(wantFiles) {
				t.Fatalf("files = %#v, found=%v, err=%v", files, found, err)
			}
			gotFiles := map[string]string{}
			for _, entry := range files {
				file := entry.(map[string]any)
				path, _, _ := unstructured.NestedString(file, "path")
				source, _, _ := unstructured.NestedString(file, "contents", "source")
				gotFiles[path] = source
			}
			if !maps.Equal(gotFiles, wantFiles) {
				t.Errorf("files = %#v, want %#v", gotFiles, wantFiles)
			}
		})
	}
}
