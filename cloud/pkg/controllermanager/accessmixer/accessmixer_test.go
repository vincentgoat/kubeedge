package accessmixer

import (
	"sort"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policyv1alpha1 "github.com/kubeedge/kubeedge/pkg/apis/policy/v1alpha1"
)

var module = `
		package example
		import data.bindings
		import data.roles
		default allow = false
		allow {
			user_has_role[role_name]
			role_has_permission[role_name]
		}
		user_has_role[role_name] {
			b = bindings[_]
			b.role = role_name
			b.user = input.subject.user
		}
		role_has_permission[role_name] {
			r = roles[_]
			r.name = role_name
			match_with_wildcard(r.operations, input.operation)
			match_with_wildcard(r.resources, input.resource)
		}
		match_with_wildcard(allowed, value) {
			allowed[_] = "*"
		}
		match_with_wildcard(allowed, value) {
			allowed[_] = value
		}
	`

var initAccessMixer = policyv1alpha1.AccessMixer{
	ObjectMeta: metav1.ObjectMeta{
		Name: "test",
	},
	Spec: policyv1alpha1.AccessMixerSpec{
		ServiceAccount: "test-sa",
		AccessRoleBinding: []policyv1alpha1.AccessRoleBinding{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rb",
					Namespace: "default",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind: "ServiceAccount",
						Name: "test-sa",
					},
				},
				RolePolicy: policyv1alpha1.RolePolicy{
					TypeMeta: metav1.TypeMeta{
						Kind: "Role",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-role",
						Namespace: "default",
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{"*"},
							Resources: []string{"pods", "nodes"},
							Verbs:     []string{"get", "list"},
						},
					},
				},
			},
		},
	},
}

func TestRbacPolicy(t *testing.T) {
	var updateRb1 = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: "test-sa",
		},
		{
			Kind: "ServiceAccount",
			Name: "test-sa1",
		},
	}
	cases := map[string]struct {
		roleBinding rbacv1.RoleBinding
		want        rbacv1.RoleBinding
	}{
		"update-rb": {
			roleBinding: rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rb",
					Namespace: "default",
				},
				Subjects: updateRb1,
				RoleRef: rbacv1.RoleRef{
					Kind: "Role",
					Name: "test-role",
				},
			},
			want: rbacv1.RoleBinding{},
		},
	}

	for n, c := range cases {
		results := nodesUnion(c.list1, c.list2)
		sort.Slice(results, func(i, j int) bool {
			return results[i].Name < results[j].Name
		})
		if !equality.Semantic.DeepEqual(results, c.want) {
			t.Errorf("failed at case: %s, want: %v, got: %v", n, c.want, results)
		}
	}
}
