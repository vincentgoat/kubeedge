package utils

import (
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/user"
	rbaclisters "k8s.io/client-go/listers/rbac/v1"
	rbacregistryvalidation "k8s.io/kubernetes/pkg/registry/rbac/validation"
)

type RoleGetter struct {
	Lister rbaclisters.RoleLister
}

func (g *RoleGetter) GetRole(namespace, name string) (*rbacv1.Role, error) {
	return g.Lister.Roles(namespace).Get(name)
}

type RoleBindingLister struct {
	Lister rbaclisters.RoleBindingLister
}

func (l *RoleBindingLister) ListRoleBindings(namespace string) ([]*rbacv1.RoleBinding, error) {
	return l.Lister.RoleBindings(namespace).List(labels.Everything())
}

type ClusterRoleGetter struct {
	Lister rbaclisters.ClusterRoleLister
}

func (g *ClusterRoleGetter) GetClusterRole(name string) (*rbacv1.ClusterRole, error) {
	return g.Lister.Get(name)
}

type ClusterRoleBindingLister struct {
	Lister rbaclisters.ClusterRoleBindingLister
}

func (l *ClusterRoleBindingLister) ListClusterRoleBindings() ([]*rbacv1.ClusterRoleBinding, error) {
	return l.Lister.List(labels.Everything())
}

var defaultRuleResolver = rbacregistryvalidation.NewDefaultRuleResolver(
	&RoleGetter{},
	&RoleBindingLister{},
	&ClusterRoleGetter{},
	&ClusterRoleBindingLister{},
)

func GenerateAuthPolicy(user user.Info, namespace string) *rbacv1.PolicyRule {

	return &rbacv1.PolicyRule{
		Verbs:     []string{"*"},
		APIGroups: []string{"*"},
		Resources: []string{"*"},
	}
}
