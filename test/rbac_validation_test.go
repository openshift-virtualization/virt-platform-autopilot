package test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

var _ = Describe("RBAC Permissions Validation", func() {
	// These tests verify that the static RBAC role has core permissions needed by the operator
	// Note: Permissions for managed resource types (MachineConfig, MetalLB, etc.) will be
	// generated dynamically in the future, so we only test truly static permissions here

	var clusterRole *rbacv1.ClusterRole

	BeforeEach(func() {
		By("loading the ClusterRole from config/rbac/role.yaml")
		rolePath := filepath.Join("..", "config", "rbac", "role.yaml")
		roleBytes, err := os.ReadFile(rolePath)
		Expect(err).NotTo(HaveOccurred(), "Should be able to read role.yaml")

		clusterRole = &rbacv1.ClusterRole{}
		err = yaml.Unmarshal(roleBytes, clusterRole)
		Expect(err).NotTo(HaveOccurred(), "Should be able to parse role.yaml")
		Expect(clusterRole.Rules).NotTo(BeEmpty(), "ClusterRole should have rules")
	})

	It("should have permissions for HyperConverged", func() {
		By("checking HyperConverged permissions")

		found := false
		requiredVerbs := []string{"get", "list", "watch", "create", "update", "patch"}

		for _, rule := range clusterRole.Rules {
			hasAPIGroup := false
			for _, apiGroup := range rule.APIGroups {
				if apiGroup == "hco.kubevirt.io" {
					hasAPIGroup = true
					break
				}
			}

			if !hasAPIGroup {
				continue
			}

			hasResource := false
			for _, resource := range rule.Resources {
				if resource == "hyperconvergeds" {
					hasResource = true
					break
				}
			}

			if !hasResource {
				continue
			}

			// Check for all required verbs
			hasAllVerbs := true
			for _, requiredVerb := range requiredVerbs {
				verbFound := false
				for _, verb := range rule.Verbs {
					if verb == requiredVerb {
						verbFound = true
						break
					}
				}
				if !verbFound {
					hasAllVerbs = false
					break
				}
			}

			if hasAllVerbs {
				found = true
				break
			}
		}

		Expect(found).To(BeTrue(), "ClusterRole should have full permissions for HyperConverged")
	})

	It("should have permissions for CRD discovery", func() {
		By("checking CustomResourceDefinition permissions")

		found := false
		requiredVerbs := []string{"get", "list", "watch"}

		for _, rule := range clusterRole.Rules {
			hasAPIGroup := false
			for _, apiGroup := range rule.APIGroups {
				if apiGroup == "apiextensions.k8s.io" {
					hasAPIGroup = true
					break
				}
			}

			if !hasAPIGroup {
				continue
			}

			hasResource := false
			for _, resource := range rule.Resources {
				if resource == "customresourcedefinitions" {
					hasResource = true
					break
				}
			}

			if !hasResource {
				continue
			}

			// Check for required verbs (read-only for CRDs)
			hasAllVerbs := true
			for _, requiredVerb := range requiredVerbs {
				verbFound := false
				for _, verb := range rule.Verbs {
					if verb == requiredVerb {
						verbFound = true
						break
					}
				}
				if !verbFound {
					hasAllVerbs = false
					break
				}
			}

			if hasAllVerbs {
				found = true
				break
			}
		}

		Expect(found).To(BeTrue(), "ClusterRole should have read permissions for CRDs")
	})

	It("should have permissions for Nodes (hardware detection)", func() {
		By("checking Node permissions")

		found := false
		requiredVerbs := []string{"get", "list", "watch"}

		for _, rule := range clusterRole.Rules {
			hasAPIGroup := false
			for _, apiGroup := range rule.APIGroups {
				if apiGroup == "" { // Core API group
					hasAPIGroup = true
					break
				}
			}

			if !hasAPIGroup {
				continue
			}

			hasResource := false
			for _, resource := range rule.Resources {
				if resource == "nodes" {
					hasResource = true
					break
				}
			}

			if !hasResource {
				continue
			}

			// Check for required verbs (read-only for nodes)
			hasAllVerbs := true
			for _, requiredVerb := range requiredVerbs {
				verbFound := false
				for _, verb := range rule.Verbs {
					if verb == requiredVerb {
						verbFound = true
						break
					}
				}
				if !verbFound {
					hasAllVerbs = false
					break
				}
			}

			if hasAllVerbs {
				found = true
				break
			}
		}

		Expect(found).To(BeTrue(), "ClusterRole should have read permissions for Nodes")
	})

	It("should have permissions for Events", func() {
		By("checking Event permissions")

		found := false
		requiredVerbs := []string{"create", "patch"}

		for _, rule := range clusterRole.Rules {
			hasAPIGroup := false
			for _, apiGroup := range rule.APIGroups {
				if apiGroup == "" { // Core API group
					hasAPIGroup = true
					break
				}
			}

			if !hasAPIGroup {
				continue
			}

			hasResource := false
			for _, resource := range rule.Resources {
				if resource == "events" {
					hasResource = true
					break
				}
			}

			if !hasResource {
				continue
			}

			// Check for required verbs (write-only for events)
			hasAllVerbs := true
			for _, requiredVerb := range requiredVerbs {
				verbFound := false
				for _, verb := range rule.Verbs {
					if verb == requiredVerb {
						verbFound = true
						break
					}
				}
				if !verbFound {
					hasAllVerbs = false
					break
				}
			}

			if hasAllVerbs {
				found = true
				break
			}
		}

		Expect(found).To(BeTrue(), "ClusterRole should have create/patch permissions for Events")
	})

	It("should have permissions for Leader Election", func() {
		By("checking Lease permissions for leader election")

		found := false
		requiredVerbs := []string{"get", "list", "watch", "create", "update", "patch", "delete"}

		for _, rule := range clusterRole.Rules {
			hasAPIGroup := false
			for _, apiGroup := range rule.APIGroups {
				if apiGroup == "coordination.k8s.io" {
					hasAPIGroup = true
					break
				}
			}

			if !hasAPIGroup {
				continue
			}

			hasResource := false
			for _, resource := range rule.Resources {
				if resource == "leases" {
					hasResource = true
					break
				}
			}

			if !hasResource {
				continue
			}

			// Check for all required verbs
			hasAllVerbs := true
			for _, requiredVerb := range requiredVerbs {
				verbFound := false
				for _, verb := range rule.Verbs {
					if verb == requiredVerb {
						verbFound = true
						break
					}
				}
				if !verbFound {
					hasAllVerbs = false
					break
				}
			}

			if hasAllVerbs {
				found = true
				break
			}
		}

		Expect(found).To(BeTrue(), "ClusterRole should have full permissions for Leases (leader election)")
	})

})
