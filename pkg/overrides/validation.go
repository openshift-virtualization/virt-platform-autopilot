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

package overrides

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// AnnotationAutopilotEnabled is the opt-in annotation on the HCO CR to activate the autopilot.
	// In the current early phase, the autopilot is inactive unless this annotation is set to "true".
	// This behavior will be inverted in a future release once the project matures.
	AnnotationAutopilotEnabled = "platform.kubevirt.io/autopilot"

	// AnnotationMode is the annotation key for management mode (managed/unmanaged)
	AnnotationMode = "platform.kubevirt.io/mode"

	// ModeUnmanaged indicates the autopilot should not manage this resource
	ModeUnmanaged = "unmanaged"

	// AnnotationReconcilePaused is set when an edit war is detected
	// The operator will skip reconciliation while this annotation is present
	AnnotationReconcilePaused = "platform.kubevirt.io/reconcile-paused"
)

var (
	// sensitiveKinds defines resource kinds where JSON patches are blocked for security
	// These resources have elevated privileges or control cluster security
	sensitiveKinds = map[string]bool{
		// Machine configuration - controls node-level config
		"MachineConfig": true,
		"KubeletConfig": true,

		// RBAC resources - control permissions
		"ClusterRole":        true,
		"ClusterRoleBinding": true,
		"Role":               true,
		"RoleBinding":        true,
		"ServiceAccount":     true,

		// Security and admission control
		"PodSecurityPolicy":              true,
		"SecurityContextConstraints":     true,
		"ValidatingWebhookConfiguration": true,
		"MutatingWebhookConfiguration":   true,

		// Note: We intentionally allow patching NodeHealthCheck to let users customize
		// remediation settings, but we could add it here if needed
	}
)

// ValidatePatchSecurity validates that a JSON patch is safe to apply
// Blocks patches on sensitive resource kinds to prevent privilege escalation
func ValidatePatchSecurity(obj *unstructured.Unstructured) error {
	if obj == nil {
		return fmt.Errorf("object is nil")
	}

	kind := obj.GetKind()
	if sensitiveKinds[kind] {
		annotations := obj.GetAnnotations()
		if annotations != nil {
			if _, hasPatch := annotations[PatchAnnotation]; hasPatch {
				return fmt.Errorf("JSON patches are not allowed on sensitive resource kind: %s", kind)
			}
		}
	}

	return nil
}

// IsUnmanaged checks if a resource has the unmanaged annotation
func IsUnmanaged(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}

	mode, exists := annotations[AnnotationMode]
	return exists && mode == ModeUnmanaged
}

// IsPaused checks if a resource has the reconcile-paused annotation
// This annotation is set when an edit war is detected
func IsPaused(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}

	val, exists := annotations[AnnotationReconcilePaused]
	return exists && val == "true"
}

// IsAutopilotEnabled checks if the autopilot is opted in via the HCO CR annotation.
// In the current early phase, the autopilot is inactive unless this annotation is
// explicitly set to "true". This behavior will be inverted in a future release.
func IsAutopilotEnabled(hco *unstructured.Unstructured) bool {
	if hco == nil {
		return false
	}

	annotations := hco.GetAnnotations()
	if annotations == nil {
		return false
	}

	return annotations[AnnotationAutopilotEnabled] == "true"
}

// ValidateAnnotations validates all override annotations on an object
func ValidateAnnotations(obj *unstructured.Unstructured) error {
	if obj == nil {
		return fmt.Errorf("object is nil")
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}

	// Validate patch annotation
	if patchStr, exists := annotations[PatchAnnotation]; exists {
		if err := ValidateJSONPatch(patchStr); err != nil {
			return fmt.Errorf("invalid patch annotation: %w", err)
		}

		// Check security restrictions
		if err := ValidatePatchSecurity(obj); err != nil {
			return err
		}
	}

	// Validate ignore-fields annotation
	if ignoreFields, exists := annotations[AnnotationIgnoreFields]; exists {
		if err := ValidatePointers(ignoreFields); err != nil {
			return fmt.Errorf("invalid ignore-fields annotation: %w", err)
		}
	}

	// Validate mode annotation
	if mode, exists := annotations[AnnotationMode]; exists {
		if mode != ModeUnmanaged && mode != "" {
			return fmt.Errorf("invalid mode annotation: %s (must be 'unmanaged' or empty)", mode)
		}
	}

	return nil
}
