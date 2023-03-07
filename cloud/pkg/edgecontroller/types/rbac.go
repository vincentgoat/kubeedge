package types

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RoleGetter interface {
	GetRole(namespace, name string) (*rbacv1.Role, error)
}

type RoleBindingLister interface {
	ListRoleBindings(namespace string) ([]*rbacv1.RoleBinding, error)
}

type ClusterRoleGetter interface {
	GetClusterRole(name string) (*rbacv1.ClusterRole, error)
}

type ClusterRoleBindingLister interface {
	ListClusterRoleBindings() ([]*rbacv1.ClusterRoleBinding, error)
}

// AuthPolicyResolver is used to resolve the authorization policy for a given user.
type AuthPolicyResolver struct {
	RoleGetter               RoleGetter
	RoleBindingLister        RoleBindingLister
	ClusterRoleGetter        ClusterRoleGetter
	ClusterRoleBindingLister ClusterRoleBindingLister
}

type AuthPolicy struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Subjects holds references to the objects the role applies to.
	// +optional
	Subjects []rbacv1.Subject `json:"subjects,omitempty" protobuf:"bytes,2,rep,name=subjects"`

	// RoleRef can only reference a ClusterRole in the global namespace.
	// If the RoleRef cannot be resolved, the Authorizer must return an error.
	RoleRef rbacv1.RoleRef `json:"roleRef" protobuf:"bytes,3,opt,name=roleRef"`

	// Rules holds all the PolicyRules for this Role
	// +optional
	Rules []rbacv1.PolicyRule `json:"rules" protobuf:"bytes,4,rep,name=rules"`
}
