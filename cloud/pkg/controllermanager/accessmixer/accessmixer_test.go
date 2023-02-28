package accessmixer

import (
	"sort"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestRbacPolicy(t *testing.T) {
	cases := map[string]struct {
		roleBinding rbacv1.RoleBinding
		want        rbacv1.RoleBinding
	}{
		"update-rb": {
			roleBinding: rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Subjects: []rbacv1.Subject{
					{ // This is the subject we want to update
						Kind: "User",
						Name: "test",
					},
				},
				RoleRef: rbacv1.RoleRef{
					Kind: "ClusterRole",
					Name: "test",
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
