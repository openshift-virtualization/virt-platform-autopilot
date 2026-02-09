package test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// CRDSet represents a collection of CRDs that can be installed together
type CRDSet string

const (
	// CRD sets for testing different scenarios
	CRDSetCore        CRDSet = "kubevirt"    // HCO CRD (always loaded in BeforeSuite)
	CRDSetOpenShift   CRDSet = "openshift"   // MachineConfig CRDs
	CRDSetRemediation CRDSet = "remediation" // NodeHealthCheck, SNR, FAR CRDs
	CRDSetOperators   CRDSet = "operators"   // MTV, MetalLB CRDs
)

// InstallCRDs installs a CRD set dynamically during test execution
// This simulates the scenario where CRDs are installed after the operator starts
// This function is idempotent - it's safe to call multiple times
func InstallCRDs(ctx context.Context, c client.Client, crdSet CRDSet) error {
	crdDir := filepath.Join("..", "assets", "crds", string(crdSet))

	// Check if directory exists
	if _, err := os.Stat(crdDir); os.IsNotExist(err) {
		return fmt.Errorf("CRD directory does not exist: %s", crdDir)
	}

	// Read all CRD files from the directory
	files, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to list CRD files: %w", err)
	}

	for _, file := range files {
		if err := installCRDFile(ctx, c, file); err != nil {
			return fmt.Errorf("failed to install CRD from %s: %w", file, err)
		}
	}

	return nil
}

// installCRDFile reads and installs a single CRD file
func installCRDFile(ctx context.Context, c client.Client, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read CRD file: %w", err)
	}

	// Parse YAML to unstructured
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, obj); err != nil {
		return fmt.Errorf("failed to parse CRD YAML: %w", err)
	}

	// Verify it's a CRD
	if obj.GetKind() != "CustomResourceDefinition" {
		return fmt.Errorf("file is not a CRD, got kind: %s", obj.GetKind())
	}

	crdName := obj.GetName()

	// Check if CRD already exists
	existing := &apiextensionsv1.CustomResourceDefinition{}
	key := client.ObjectKey{Name: crdName}
	err = c.Get(ctx, key, existing)

	if err == nil {
		// CRD exists - check if it's being deleted
		if existing.DeletionTimestamp != nil {
			// CRD is being deleted, wait for it to be fully removed
			err := wait.PollUntilContextTimeout(ctx, 250*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
				checkCRD := &apiextensionsv1.CustomResourceDefinition{}
				err := c.Get(ctx, key, checkCRD)
				// CRD is gone when we get NotFound error
				return err != nil, nil
			})
			if err != nil {
				return fmt.Errorf("timeout waiting for CRD %s to be deleted: %w", crdName, err)
			}
			// CRD is now deleted, proceed with creation
		} else {
			// CRD exists and is not being deleted - it's already installed
			// Just wait for it to be established (in case it was just created)
			return waitForCRDEstablished(ctx, c, crdName)
		}
	}

	// Create the CRD (either it didn't exist, or we waited for deletion to complete)
	if err := c.Create(ctx, obj); err != nil {
		return fmt.Errorf("failed to create CRD: %w", err)
	}

	// Wait for CRD to be established
	return waitForCRDEstablished(ctx, c, crdName)
}

// waitForCRDEstablished waits for a CRD to become established
func waitForCRDEstablished(ctx context.Context, c client.Client, crdName string) error {
	return wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		key := client.ObjectKey{Name: crdName}

		if err := c.Get(ctx, key, crd); err != nil {
			return false, err
		}

		// Check if CRD is established
		for _, condition := range crd.Status.Conditions {
			if condition.Type == apiextensionsv1.Established {
				return condition.Status == apiextensionsv1.ConditionTrue, nil
			}
		}
		return false, nil
	})
}

// UninstallCRDs removes a CRD set (useful for testing missing CRD scenarios)
// This function is idempotent - it's safe to call multiple times
func UninstallCRDs(ctx context.Context, c client.Client, crdSet CRDSet) error {
	crdDir := filepath.Join("..", "assets", "crds", string(crdSet))
	files, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to list CRD files: %w", err)
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(data, obj); err != nil {
			continue
		}

		// Delete the CRD (ignore errors if already deleted)
		_ = c.Delete(ctx, obj)
	}

	return nil
}

// IsCRDInstalled checks if a specific CRD is installed in the cluster
// Returns false if CRD is being deleted
func IsCRDInstalled(ctx context.Context, c client.Client, crdName string) bool {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	key := client.ObjectKey{Name: crdName}
	err := c.Get(ctx, key, crd)
	if err != nil {
		return false
	}
	// Don't count CRDs that are being deleted as "installed"
	return crd.DeletionTimestamp == nil
}

// WaitForCRD waits for a CRD to be installed and established
// This is useful for testing dynamic CRD installation scenarios
func WaitForCRD(ctx context.Context, c client.Client, crdName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, timeout, true, func(ctx context.Context) (bool, error) {
		return IsCRDInstalled(ctx, c, crdName), nil
	})
}

// ExpectCRDInstalled is a Gomega matcher helper for checking CRD installation
func ExpectCRDInstalled(ctx context.Context, c client.Client, crdName string) {
	EventuallyWithOffset(1, func() bool {
		return IsCRDInstalled(ctx, c, crdName)
	}, 10*time.Second, 250*time.Millisecond).Should(BeTrue(),
		fmt.Sprintf("CRD %s should be installed", crdName))
}

// ExpectCRDNotInstalled is a Gomega matcher helper for checking CRD is NOT installed
func ExpectCRDNotInstalled(ctx context.Context, c client.Client, crdName string) {
	ConsistentlyWithOffset(1, func() bool {
		return IsCRDInstalled(ctx, c, crdName)
	}, 2*time.Second, 250*time.Millisecond).Should(BeFalse(),
		fmt.Sprintf("CRD %s should NOT be installed", crdName))
}
