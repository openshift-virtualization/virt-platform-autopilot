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

package test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

const runbookRequestTimeout = 15 * time.Second

// RUNBOOKS_DIR validates a companion monitoring checkout before publication.
// CI defaults to the published runbooks, matching the SSP availability check.
var _ = Describe("Alert runbooks", func() {
	It("has an available dedicated runbook for every alert", func() {
		registry, err := assets.NewRegistry(assets.NewLoader())
		Expect(err).NotTo(HaveOccurred())
		asset, err := registry.GetAsset("prometheus-alerts")
		Expect(err).NotTo(HaveOccurred())
		rule, err := engine.NewRenderer(assets.NewLoader()).RenderAsset(asset, &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("hco", "openshift-cnv"),
		})
		Expect(err).NotTo(HaveOccurred())
		groups, found, err := unstructured.NestedSlice(rule.Object, "spec", "groups")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		httpClient := &http.Client{Timeout: runbookRequestTimeout}
		for _, group := range groups {
			for _, entry := range group.(map[string]any)["rules"].([]any) {
				alert := entry.(map[string]any)
				name, isAlert := alert["alert"].(string)
				if !isAlert {
					continue
				}
				annotations, ok := alert["annotations"].(map[string]any)
				Expect(ok).To(BeTrue(), "alert %s has no annotations", name)
				url, _ := annotations["runbook_url"].(string)
				Expect(url).To(Equal(fmt.Sprintf(pkgcontext.DefaultRunbookURLTemplate, name)))
				Expect(validateRunbook(httpClient, name, url, os.Getenv("RUNBOOKS_DIR"))).To(Succeed(), "alert %s has no associated runbook", name)
			}
		}
	})
})

func validateRunbook(httpClient *http.Client, name, url, directory string) error {
	if name == "" || url == "" || !strings.HasSuffix(url, "/"+name) {
		return fmt.Errorf("%s must reference its dedicated runbook", name)
	}
	if directory != "" {
		data, err := os.ReadFile(filepath.Join(directory, name+".md"))
		if err != nil {
			return fmt.Errorf("%s runbook: %w", name, err)
		}
		text := string(data)
		for _, heading := range []string{"# " + name, "## Meaning", "## Impact", "## Diagnosis", "## Mitigation"} {
			if !strings.Contains("\n"+text, "\n"+heading+"\n") {
				return fmt.Errorf("%s runbook is missing %s", name, heading)
			}
		}
		return nil
	}
	response, err := httpClient.Head(url)
	if err != nil {
		return fmt.Errorf("%s runbook: %w", name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s runbook returned HTTP %d", name, response.StatusCode)
	}
	return nil
}

func TestRunbookAvailabilityValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/Missing" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, tt := range []struct {
		name, url string
		wantError bool
	}{
		{"Available", server.URL + "/Available", false},
		{"Missing", server.URL + "/Missing", true},
		{"Available", "", true},
		{"Available", server.URL + "/Other", true},
	} {
		err := validateRunbook(server.Client(), tt.name, tt.url, "")
		if (err != nil) != tt.wantError {
			t.Fatalf("%s: error = %v, wantError %t", tt.name, err, tt.wantError)
		}
	}
	directory := t.TempDir()
	if err := validateRunbook(server.Client(), "Missing", server.URL+"/Missing", directory); err == nil {
		t.Fatal("missing local runbook passed")
	}
	path := filepath.Join(directory, "Available.md")
	if err := os.WriteFile(path, []byte("# Available\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateRunbook(server.Client(), "Available", server.URL+"/Available", directory); err == nil {
		t.Fatal("incomplete local runbook passed")
	}
	text := "# Available\n\n## Meaning\n\n## Impact\n\n## Diagnosis\n\n## Mitigation\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateRunbook(server.Client(), "Available", server.URL+"/Available", directory); err != nil {
		t.Fatal(err)
	}
}
