/*
Copyright 2026.

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

// Package v1alpha1 contains API Schema definitions for the r8r.io v1alpha1 API group.
//
// The group is intentionally the bare domain `r8r.io` (no subgroup prefix), so
// objects are addressed as `apiVersion: r8r.io/v1alpha1`. This is the operator's
// canonical API surface: Replication (the operator-owned materialization of a
// replication request) and ReplicationPolicy (the admin-controlled security
// boundary).
//
// +kubebuilder:object:generate=true
// +groupName=r8r.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the group version used to register these objects.
	// Group is exactly "r8r.io" — the bare project domain, by design (D10).
	GroupVersion = schema.GroupVersion{Group: "r8r.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, objectTypes...)
		metav1.AddToGroupVersion(s, GroupVersion)
		return nil
	})

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme

	// objectTypes collects the Go types registered by each *_types.go init().
	objectTypes = []runtime.Object{}
)
