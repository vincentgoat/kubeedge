package utils

import (
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	rbaclisters "k8s.io/client-go/listers/rbac/v1"

	"github.com/kubeedge/kubeedge/cloud/pkg/edgecontroller/types"
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

// AuthPolicyResolver is used to resolve the authorization policy for a given user.
type AuthPolicyResolver struct {
	RoleGetter               types.RoleGetter
	RoleBindingLister        types.RoleBindingLister
	ClusterRoleGetter        types.ClusterRoleGetter
	ClusterRoleBindingLister types.ClusterRoleBindingLister
}

func appliesToUser(user user.Info, subject rbacv1.Subject, namespace string) bool {
	switch subject.Kind {
	case rbacv1.UserKind:
		return user.GetName() == subject.Name

	case rbacv1.GroupKind:
		return has(user.GetGroups(), subject.Name)

	case rbacv1.ServiceAccountKind:
		// default the namespace to namespace we're working in if its available.  This allows rolebindings that reference
		// SAs in th local namespace to avoid having to qualify them.
		saNamespace := namespace
		if len(subject.Namespace) > 0 {
			saNamespace = subject.Namespace
		}
		if len(saNamespace) == 0 {
			return false
		}
		// use a more efficient comparison for RBAC checking
		return serviceaccount.MatchesUsername(saNamespace, subject.Name, user.GetName())
	default:
		return false
	}
}

// appliesTo returns whether any of the bindingSubjects applies to the specified subject,
// and if true, the index of the first subject that applies
func appliesTo(user user.Info, bindingSubjects []rbacv1.Subject, namespace string) (int, bool) {
	for i, bindingSubject := range bindingSubjects {
		if appliesToUser(user, bindingSubject, namespace) {
			return i, true
		}
	}
	return 0, false
}

func has(set []string, ele string) bool {
	for _, s := range set {
		if s == ele {
			return true
		}
	}
	return false
}

// GetRoleReferenceRules attempts to resolve the RoleBinding or ClusterRoleBinding.
func (a *AuthPolicyResolver) GetRoleReferenceRules(roleRef rbacv1.RoleRef, bindingNamespace string) ([]rbacv1.PolicyRule, error) {
	switch roleRef.Kind {
	case "Role":
		role, err := a.RoleGetter.GetRole(bindingNamespace, roleRef.Name)
		if err != nil {
			return nil, err
		}
		return role.Rules, nil

	case "ClusterRole":
		clusterRole, err := a.ClusterRoleGetter.GetClusterRole(roleRef.Name)
		if err != nil {
			return nil, err
		}
		return clusterRole.Rules, nil

	default:
		return nil, fmt.Errorf("unsupported role reference kind: %q", roleRef.Kind)
	}
}

func (a *AuthPolicyResolver) VisitRulesFor(user user.Info, namespace string, visitor func(binding interface{}, rule *[]rbacv1.PolicyRule, err error)) {
	if clusterRoleBindings, err := a.ClusterRoleBindingLister.ListClusterRoleBindings(); err != nil {
		visitor(nil, nil, err)
		return
	} else {
		for _, clusterRoleBinding := range clusterRoleBindings {
			_, applies := appliesTo(user, clusterRoleBinding.Subjects, "")
			if !applies {
				continue
			}
			rules, err := a.GetRoleReferenceRules(clusterRoleBinding.RoleRef, "")
			if err != nil {
				visitor(nil, nil, err)
				return
			}
			visitor(clusterRoleBinding, &rules, nil)
		}
	}

	if len(namespace) > 0 {
		if roleBindings, err := a.RoleBindingLister.ListRoleBindings(namespace); err != nil {
			visitor(nil, nil, err)
			return
		} else {
			for _, roleBinding := range roleBindings {
				_, applies := appliesTo(user, roleBinding.Subjects, namespace)
				if !applies {
					continue
				}
				rules, err := a.GetRoleReferenceRules(roleBinding.RoleRef, namespace)
				if err != nil {
					visitor(nil, nil, err)
					return
				}
				visitor(roleBinding, &rules, nil)
			}
		}
	}
}

type AuthorizingVisitor struct {
	AuthPolicy []types.AuthPolicy
	Error      error
}

func (v *AuthorizingVisitor) Visit(binding interface{}, rule *[]rbacv1.PolicyRule, err error) {
	switch binding.(type) {
	case rbacv1.RoleBinding:
		bind := binding.(rbacv1.RoleBinding)
		v.AuthPolicy = append(v.AuthPolicy, types.AuthPolicy{
			Binding: types.BindingAttr{
				Name:      bind.Name,
				Namespace: bind.Namespace,
				Kind:      bind.Kind,
			},
			RoleRef:  bind.RoleRef,
			Subjects: bind.Subjects,
			Rules:    *rule,
		})
	case rbacv1.ClusterRoleBinding:
		cb := binding.(rbacv1.ClusterRoleBinding)
		v.AuthPolicy = append(v.AuthPolicy, types.AuthPolicy{
			Binding: types.BindingAttr{
				Name: cb.Name,
				Kind: cb.Kind,
			},
			RoleRef:  cb.RoleRef,
			Subjects: cb.Subjects,
			Rules:    *rule,
		})
	}
	if err != nil {
		v.Error = err
	}
}
