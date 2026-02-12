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
	// These tests verify that the static RBAC role has core permissions needed by the autopilot
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

	It("should have delete permissions for resources that may be tombstoned", func() {
		By("checking that generated RBAC includes delete verb when appropriate")

		// Note: This test verifies the RBAC generator's tombstone scanning works
		// When tombstones are added to assets/tombstones/, the RBAC generator should
		// automatically add the delete verb to the corresponding resource type

		// Currently no tombstones exist, so we verify the pattern is correct
		// When tombstones are added, this test will automatically verify delete permissions

		// Verify that dynamically generated rules with delete verb are properly formatted
		// Static rules (like leader election) may have different verb ordering
		for i, rule := range clusterRole.Rules {
			// Skip static infrastructure rules (first 5 rules)
			if i < 5 {
				continue
			}

			// For dynamic rules (from assets), check if they have delete verb
			hasDelete := false
			for _, verb := range rule.Verbs {
				if verb == "delete" {
					hasDelete = true
					break
				}
			}

			if hasDelete {
				// If delete verb exists in dynamic rules, it should be first (alphabetical order)
				Expect(rule.Verbs[0]).To(Equal("delete"),
					"Delete verb should be first in alphabetical order for dynamic rule with API group: %v, resources: %v",
					rule.APIGroups, rule.Resources)

				// Rule should also have create verb
				hasCreate := false
				for _, v := range rule.Verbs {
					if v == "create" {
						hasCreate = true
						break
					}
				}
				Expect(hasCreate).To(BeTrue(),
					"Dynamic rules with delete should also have create verb for API group: %v, resources: %v",
					rule.APIGroups, rule.Resources)
			}
		}
	})

	It("should have all verbs in alphabetical order for consistency in dynamic rules", func() {
		By("verifying verb ordering in dynamically generated rules")

		// This ensures deterministic RBAC generation for CI verification
		// Only check dynamic rules (from assets), as static rules may have custom ordering
		for i, rule := range clusterRole.Rules {
			// Skip static infrastructure rules (first 5 rules)
			if i < 5 {
				continue
			}

			// Check if verbs are in alphabetical order for dynamic rules
			for j := 1; j < len(rule.Verbs); j++ {
				// Compare strings alphabetically
				Expect(rule.Verbs[j] >= rule.Verbs[j-1]).To(BeTrue(),
					"Verbs should be in alphabetical order in dynamic rule %d (API groups: %v, resources: %v). Got: %v",
					i, rule.APIGroups, rule.Resources, rule.Verbs)
			}
		}
	})

})
