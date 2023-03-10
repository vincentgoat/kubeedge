package types

import (
	rbacv1 "k8s.io/api/rbac/v1"
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

type BindingAttr struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind"`
}

type AuthPolicy struct {
	Binding BindingAttr
	// Subjects holds references to the objects the role applies to.
	// +optional
	Subjects []rbacv1.Subject `json:"subjects,omitempty"`

	// RoleRef can only reference a ClusterRole in the global namespace.
	// If the RoleRef cannot be resolved, the Authorizer must return an error.
	RoleRef rbacv1.RoleRef `json:"roleRef"`

	// Rules holds all the PolicyRules for this Role
	// +optional
	Rules []rbacv1.PolicyRule `json:"rules"`
}
