/*
Copyright 2023 The KubeEdge Authors.

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=accm

// AccessMixer is the Schema for the AccessMixer API
type AccessMixer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec represents the specification of rbac.
	// +required
	Spec AccessMixerSpec `json:"spec,omitempty"`

	// Status represents the node list which store the rules.
	// +optional
	Status AccessMixerStatus `json:"status,omitempty"`
}

// AccessMixerStatus defines the observed state of AccessMixer
type AccessMixerStatus struct {
	// NodeList represents the node name which store the rules.
	NodeList []string `json:"nodeList,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// AccessMixerList contains a list of AccessMixer
type AccessMixerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AccessMixer `json:"items"`
}

// AccessMixerSpec defines the desired state of AccessMixerSpec
type AccessMixerSpec struct {
	// ServiceAccount is one-to-one corresponding relations with the accessmixer.
	ServiceAccount corev1.ServiceAccount `json:"serviceAccount,omitempty"`
	// AccessRoleBinding represents rbac rolebinding plus detailed role info.
	AccessRoleBinding []AccessRoleBinding `json:"accessRoleBinding,omitempty"`
	// AccessClusterRoleBinding represents rbac ClusterRoleBinding plus detailed ClusterRole info.
	AccessClusterRoleBinding []AccessClusterRoleBinding `json:"accessClusterRoleBinding,omitempty"`
	// RawExtension for extend resource.
	RawExtension []runtime.RawExtension `json:"rawExtension,omitempty"`
}

// AccessRoleBinding represents rbac rolebinding plus detailed role info.
type AccessRoleBinding struct {
	rbac.RoleBinding `json:",inline"`
	// RolePolicy contains both role and clusterrole.
	RolePolicy RolePolicy `json:"rolePolicy,omitempty" protobuf:"bytes,1,opt,name=rolePolicy"`
}

// AccessClusterRoleBinding represents rbac ClusterRoleBinding plus detailed ClusterRole info.
type AccessClusterRoleBinding struct {
	rbac.ClusterRoleBinding `json:",inline"`
	// RolePolicy contains both role and clusterrole.
	RolePolicy RolePolicy `json:"rolePolicy,omitempty" protobuf:"bytes,1,opt,name=rolePolicy"`
}

// RolePolicy contains both role and clusterrole.
type RolePolicy struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Rules holds all the PolicyRules for this Role or ClusterRole
	// +optional
	Rules []rbac.PolicyRule `json:"rules" protobuf:"bytes,2,rep,name=rules"`
	// AggregationRule is an optional field that describes how to build the Rules for the ClusterRole.
	// If AggregationRule is set, then the Rules are controller managed and direct changes to Rules will be
	// stomped by the controller.
	// +optional
	AggregationRule *rbac.AggregationRule `json:"aggregationRule,omitempty" protobuf:"bytes,3,opt,name=aggregationRule"`
}
