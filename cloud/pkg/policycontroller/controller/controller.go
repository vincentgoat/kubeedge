package controller

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/kubeedge/beehive/pkg/core/model"
	"github.com/kubeedge/kubeedge/cloud/pkg/common/messagelayer"
	"github.com/kubeedge/kubeedge/cloud/pkg/common/modules"
	"github.com/kubeedge/kubeedge/cloud/pkg/edgecontroller/constants"
	policyv1alpha1 "github.com/kubeedge/kubeedge/pkg/apis/policy/v1alpha1"
)

type Controller struct {
	client.Client
	MessageLayer messagelayer.MessageLayer
}

func (c *Controller) Reconcile(ctx context.Context, request controllerruntime.Request) (controllerruntime.Result, error) {
	am := &policyv1alpha1.AccessMixer{}
	if err := c.Client.Get(ctx, request.NamespacedName, am); err != nil {
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}
		klog.Errorf("failed to get accessmixer %s/%s, %v", request.Namespace, request.Name, err)
		return controllerruntime.Result{Requeue: true}, err
	}

	if !am.GetDeletionTimestamp().IsZero() {
		return controllerruntime.Result{}, nil
	}

	return c.syncRules(ctx, am)
}

func (c *Controller) filterResource(ctx context.Context, object client.Object) bool {
	amList := &policyv1alpha1.AccessMixerList{}
	if err := c.Client.List(ctx, amList); err != nil {
		klog.Errorf("failed to list accessmixers, %v", err)
		return true
	}
	var p = PolicyMatcher{}
	matchTarget(*amList, object, p.Visitor)
	return p.match
}

func isMatchedRoleRef(roleRef rbacv1.RoleRef, bindingNamespace string, object client.Object) bool {
	if object.GetObjectKind().GroupVersionKind().Kind != roleRef.Kind {
		return false
	}
	if roleRef.Kind == "ClusterRole" {
		return object.GetName() == roleRef.Name
	} else if roleRef.Kind == "Role" {
		return object.GetName() == roleRef.Name && bindingNamespace == object.GetNamespace()
	}
	return false
}

type PolicyMatcher struct {
	match bool
}

func (pm *PolicyMatcher) Visitor(binding interface{}, am *policyv1alpha1.AccessMixer, object client.Object) bool {
	switch object.GetObjectKind().GroupVersionKind().Kind {
	case "ClusterRole", "Role":
		switch binding.(type) {
		case *rbacv1.ClusterRoleBinding:
			if isMatchedRoleRef(binding.(*rbacv1.ClusterRoleBinding).RoleRef, "", object) {
				pm.match = true
				return false
			}

		case *rbacv1.RoleBinding:
			if isMatchedRoleRef(binding.(*rbacv1.RoleBinding).RoleRef, binding.(*rbacv1.RoleBinding).Namespace, object) {
				pm.match = true
				return false
			}
		}

	case "ClusterRoleBinding", "RoleBinding":
		switch binding.(type) {
		case *rbacv1.ClusterRoleBinding:
			if binding.(*rbacv1.ClusterRoleBinding).Name == object.GetName() {
				pm.match = true
				return false
			}
		case *rbacv1.RoleBinding:
			if binding.(*rbacv1.RoleBinding).Name == object.GetName() && binding.(*rbacv1.RoleBinding).Namespace == object.GetNamespace() {
				pm.match = true
				return false
			}
		}
	}
	return true
}

type PolicyRequestVisitor struct {
	AuthPolicy []controllerruntime.Request
}

func (p *PolicyRequestVisitor) Visitor(binding interface{}, am *policyv1alpha1.AccessMixer, object client.Object) bool {
	switch object.GetObjectKind().GroupVersionKind().Kind {
	case "ClusterRole", "Role":
		switch binding.(type) {
		case *rbacv1.ClusterRoleBinding:
			if isMatchedRoleRef(binding.(*rbacv1.ClusterRoleBinding).RoleRef, "", object) {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: am.Namespace, Name: am.Name}})
			}

		case *rbacv1.RoleBinding:
			if isMatchedRoleRef(binding.(*rbacv1.RoleBinding).RoleRef, binding.(*rbacv1.RoleBinding).Namespace, object) {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: am.Namespace, Name: am.Name}})
			}
		}

	case "ClusterRoleBinding", "RoleBinding":
		switch binding.(type) {
		case *rbacv1.ClusterRoleBinding:
			if binding.(*rbacv1.ClusterRoleBinding).Name == object.GetName() {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: am.Namespace, Name: am.Name}})
			}
		case *rbacv1.RoleBinding:
			if binding.(*rbacv1.RoleBinding).Name == object.GetName() && binding.(*rbacv1.RoleBinding).Namespace == object.GetNamespace() {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: am.Namespace, Name: am.Name}})
			}
		}
	}

	return true
}

func matchTarget(amList policyv1alpha1.AccessMixerList, object client.Object, visitor func(interface{}, *policyv1alpha1.AccessMixer, client.Object) bool) {
	for _, am := range amList.Items {
		for _, binding := range am.Spec.AccessClusterRoleBinding {
			if !visitor(&binding, &am, object) {
				return
			}
		}
		for _, binding := range am.Spec.AccessRoleBinding {
			if !visitor(&binding, &am, object) {
				return
			}
		}
	}
}

func (c *Controller) mapRolesFunc(object client.Object) []controllerruntime.Request {
	amList := &policyv1alpha1.AccessMixerList{}
	if err := c.Client.List(context.Background(), amList); err != nil {
		klog.Errorf("failed to list accessmixers, %v", err)
		return nil
	}
	var p = PolicyRequestVisitor{}
	matchTarget(*amList, object, p.Visitor)
	return p.AuthPolicy
}

func newAccessMixerObject(sa corev1.ServiceAccount) client.Object {
	return &policyv1alpha1.AccessMixer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sa.GetName(),
			Namespace: sa.GetNamespace(),
		},
		Spec: policyv1alpha1.AccessMixerSpec{
			ServiceAccount: sa,
		},
	}
}

func (c *Controller) mapObjectFunc(object client.Object) []controllerruntime.Request {
	amList := &policyv1alpha1.AccessMixerList{}
	if err := c.Client.List(context.Background(), amList, &client.ListOptions{Namespace: object.GetNamespace()}); err != nil {
		klog.Errorf("failed to list accessmixers, %v", err)
		return nil
	}
	switch object.(type) {
	case *corev1.Pod:
		sa := object.(*corev1.Pod).Spec.ServiceAccountName
		for _, am := range amList.Items {
			if am.Spec.ServiceAccount.Name == sa && am.Spec.ServiceAccount.Namespace == object.GetNamespace() {
				return []controllerruntime.Request{{NamespacedName: client.ObjectKey{Namespace: am.Namespace, Name: am.Name}}}
			}
		}
		// create accessmixer if not exist when pod event triggered
		if err := c.Client.Create(context.Background(), newAccessMixerObject(corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sa,
				Namespace: object.GetNamespace(),
			},
		})); err != nil {
			klog.Errorf("failed to create accessmixer, %v", err)
			return nil
		}
	case *corev1.ServiceAccount:
		return []controllerruntime.Request{{NamespacedName: client.ObjectKey{Namespace: object.GetNamespace(), Name: object.GetName()}}}
	}

	return []controllerruntime.Request{}
}

func (c *Controller) filterObject(ctx context.Context, object client.Object) bool {
	switch object.(type) {
	case *corev1.Pod:
		if object.(*corev1.Pod).Spec.ServiceAccountName != "" && object.(*corev1.Pod).Spec.NodeName != "" {
			return true
		}
	case *corev1.ServiceAccount:
		amList := &policyv1alpha1.AccessMixerList{}
		if err := c.Client.List(context.Background(), amList, &client.ListOptions{Namespace: object.GetNamespace()}); err != nil {
			klog.Errorf("failed to list accessmixers, %v", err)
			return false
		}
		for _, am := range amList.Items {
			if am.Spec.ServiceAccount.Name == object.GetName() && am.Spec.ServiceAccount.Namespace == object.GetNamespace() {
				return true
			}
		}
	}
	return false
}

// SetupWithManager creates a controller and register to controller manager.
func (c *Controller) SetupWithManager(ctx context.Context, mgr controllerruntime.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &corev1.Pod{}, "spec.serviceAccountName", func(o client.Object) []string {
		pod := o.(*corev1.Pod)
		return []string{pod.Spec.ServiceAccountName}
	}); err != nil {
		return fmt.Errorf("failed to set ServiceAccountName field selector for manager, %v", err)
	}
	return controllerruntime.NewControllerManagedBy(mgr).
		For(&policyv1alpha1.AccessMixer{}).
		Watches(&source.Kind{Type: &rbacv1.ClusterRoleBinding{}}, handler.EnqueueRequestsFromMapFunc(c.mapRolesFunc), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return c.filterResource(ctx, object)
		}))).
		Watches(&source.Kind{Type: &rbacv1.RoleBinding{}}, handler.EnqueueRequestsFromMapFunc(c.mapRolesFunc), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return c.filterResource(ctx, object)
		}))).
		Watches(&source.Kind{Type: &rbacv1.ClusterRole{}}, handler.EnqueueRequestsFromMapFunc(c.mapRolesFunc), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return c.filterResource(ctx, object)
		}))).
		Watches(&source.Kind{Type: &rbacv1.Role{}}, handler.EnqueueRequestsFromMapFunc(c.mapRolesFunc), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return c.filterResource(ctx, object)
		}))).
		Watches(&source.Kind{Type: &corev1.ServiceAccount{}}, handler.EnqueueRequestsFromMapFunc(c.mapObjectFunc), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return c.filterObject(ctx, object)
		}))).
		Watches(&source.Kind{Type: &corev1.Pod{}}, handler.EnqueueRequestsFromMapFunc(c.mapObjectFunc), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return c.filterObject(ctx, object)
		}))).
		Complete(c)
}

func containsSubject(new []interface{}, oldItem interface{}) bool {
	for _, newItem := range new {
		if equality.Semantic.DeepEqual(newItem, oldItem) {
			return true
		}
	}
	return false
}

func compareSubjects(old, new interface{}) bool {
	switch n := new.(type) {
	case []interface{}:
		switch o := old.(type) {
		case []interface{}:
			if len(o) != len(n) {
				return false
			}
			for _, oldItem := range o {
				if !containsSubject(n, oldItem) {
					return false
				}
			}
			return true
		}
	}
	return false
}

func getNodeListOfAccessMixer(ctx context.Context, cli client.Client, am *policyv1alpha1.AccessMixer) ([]string, error) {
	var nodeList []string
	podList := &corev1.PodList{}
	saNameSelector := fields.OneTermEqualSelector("spec.serviceAccountName", am.Spec.ServiceAccount.Name)
	allSelectors := fields.AndSelectors(saNameSelector, fields.OneTermEqualSelector("metadata.namespace", am.Namespace))
	if err := cli.List(ctx, podList, client.MatchingFieldsSelector{Selector: allSelectors}); err != nil {
		klog.Errorf("failed to list pods through field selector serviceAccountName, %v", err)
		return nil, err
	}
	var nodeMap = make(map[string]bool)
	for _, pod := range podList.Items {
		if pod.Spec.NodeName == "" {
			continue
		}
		if nodeMap[pod.Spec.NodeName] {
			continue
		}
		nodeMap[pod.Spec.NodeName] = true
		nodeList = append(nodeList, pod.Spec.NodeName)
	}
	return nodeList, nil
}

func intersectSlice(old, new []string) []string {
	var intersect []string
	var oldMap = make(map[string]bool)
	for _, oldItem := range old {
		oldMap[oldItem] = true
	}
	for _, newItem := range new {
		if oldMap[newItem] {
			intersect = append(intersect, newItem)
		}
	}
	return intersect
}

func subtractSlice(source, subTarget []string) []string {
	var subtract []string
	var oldMap = make(map[string]bool)
	for _, oldItem := range source {
		oldMap[oldItem] = true
	}
	for _, newItem := range subTarget {
		if !oldMap[newItem] {
			subtract = append(subtract, newItem)
		}
	}
	return subtract
}

func (c *Controller) send2Edge(am *policyv1alpha1.AccessMixer, targets []string, opr string) {
	for _, node := range targets {
		resource, err := messagelayer.BuildResource(node, am.Namespace, model.ResourceTypeAccessRoleMixer, am.Name)
		if err != nil {
			klog.Warningf("built message resource failed with error: %s", err)
			continue
		}
		// filter out the node list data
		am.Status.NodeList = []string{}
		msg := model.NewMessage("").
			SetResourceVersion(am.ResourceVersion).
			FillBody(am).BuildRouter(modules.PolicyControllerModuleName, constants.GroupResource, resource, opr)
		if err := c.MessageLayer.Send(*msg); err != nil {
			klog.Warningf("send message %s failed with error: %s", resource, err)
			continue
		}
	}
	return
}

func (c *Controller) syncRules(ctx context.Context, am *policyv1alpha1.AccessMixer) (controllerruntime.Result, error) {
	var newSA corev1.ServiceAccount
	err := c.Client.Get(ctx, types.NamespacedName{Namespace: am.Namespace, Name: am.Spec.ServiceAccount.Name}, &newSA)
	if err != nil && apierrors.IsNotFound(err) {
		klog.V(4).Infof("serviceaccount %s/%s not found and delete the policy resource", am.Namespace, am.Spec.ServiceAccount.Name)
		if err := c.Client.Delete(ctx, am); err != nil {
			klog.Errorf("failed to delete accessmixer %s/%s, %v", am.Namespace, am.Name, err)
			return controllerruntime.Result{Requeue: true}, err
		}
		c.send2Edge(am, am.Status.NodeList, model.DeleteOperation)
		return controllerruntime.Result{}, nil
	} else if err != nil {
		klog.Errorf("failed to get serviceaccount %s/%s, %v", am.Namespace, am.Spec.ServiceAccount.Name, err)
		return controllerruntime.Result{Requeue: true}, err
	}
	var newAm = &policyv1alpha1.AccessMixer{Spec: policyv1alpha1.AccessMixerSpec{ServiceAccount: newSA}}
	userInfo := serviceaccount.UserInfo(newSA.Namespace, newSA.Name, string(newSA.UID))
	c.VisitRulesFor(ctx, userInfo, am.Namespace, newAm)
	nodes, err := getNodeListOfAccessMixer(ctx, c.Client, am)
	if err != nil {
		klog.Errorf("failed to get node list of accessmixer %s/%s, %v", am.Namespace, am.Name, err)
		return controllerruntime.Result{Requeue: true}, err
	}
	deleteNodes := subtractSlice(nodes, am.Status.NodeList)
	if len(deleteNodes) != 0 {
		// no nodes in the current am status, delete the am
		if len(nodes) == 0 {
			if err = c.Client.Delete(ctx, am); err != nil {
				klog.Errorf("failed to delete accessmixer %s/%s, %v", am.Namespace, am.Name, err)
				return controllerruntime.Result{Requeue: true}, err
			}
			c.send2Edge(am, deleteNodes, model.DeleteOperation)
			return controllerruntime.Result{}, nil
		}
		c.send2Edge(am, deleteNodes, model.DeleteOperation)
	}
	sort.Slice(am.Spec.AccessRoleBinding, func(i, j int) bool {
		return am.Spec.AccessRoleBinding[i].Name < am.Spec.AccessRoleBinding[j].Name
	})
	sort.Slice(newAm.Spec.AccessClusterRoleBinding, func(i, j int) bool {
		return newAm.Spec.AccessClusterRoleBinding[i].Name < newAm.Spec.AccessClusterRoleBinding[j].Name
	})

	if !compareSubjects(am.Spec.AccessClusterRoleBinding, newAm.Spec.AccessClusterRoleBinding) ||
		!compareSubjects(am.Spec.AccessRoleBinding, newAm.Spec.AccessRoleBinding) ||
		!equality.Semantic.DeepEqual(am.Spec.ServiceAccount, newSA) {
		am.Spec = *(newAm.Spec.DeepCopy())
		am.Status.NodeList = append([]string{}, nodes...)
		if err := c.Client.Update(ctx, am); err != nil {
			klog.Errorf("failed to update accessmixer %s/%s, %v", am.Namespace, am.Name, err)
			return controllerruntime.Result{Requeue: true}, err
		}
		c.send2Edge(am, nodes, model.UpdateOperation)
	} else {
		klog.Infof("=====accessmixer %s/%s is up to date", am.Namespace, am.Name)
	}
	return controllerruntime.Result{}, nil
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
func (c *Controller) GetRoleReferenceRules(ctx context.Context, roleRef rbacv1.RoleRef, bindingNamespace string) ([]rbacv1.PolicyRule, error) {
	switch roleRef.Kind {
	case "Role":
		var role = &rbacv1.Role{}
		err := c.Client.Get(ctx, types.NamespacedName{Namespace: bindingNamespace, Name: roleRef.Name}, role)
		if err != nil {
			return nil, err
		}
		return role.Rules, nil

	case "ClusterRole":
		var clusterRole = &rbacv1.ClusterRole{}
		err := c.Client.Get(ctx, types.NamespacedName{Name: roleRef.Name}, clusterRole)
		if err != nil {
			return nil, err
		}
		return clusterRole.Rules, nil

	default:
		return nil, fmt.Errorf("unsupported role reference kind: %q", roleRef.Kind)
	}
}

func (c *Controller) VisitRulesFor(ctx context.Context, user user.Info, namespace string, am *policyv1alpha1.AccessMixer) {
	crbl := &rbacv1.ClusterRoleBindingList{}
	if err := c.Client.List(ctx, crbl); err != nil {
		klog.Errorf("failed to list clusterrolebindings, %v", err)
		return
	} else {
		for _, crb := range crbl.Items {
			_, applies := appliesTo(user, crb.Subjects, "")
			if !applies {
				continue
			}
			rules, err := c.GetRoleReferenceRules(ctx, crb.RoleRef, "")
			if err != nil {
				klog.Errorf("failed to get rules for clusterrolebinding %s, %v", crb.Name, err)
				return
			}
			var accessClusterRoleBinding = policyv1alpha1.AccessClusterRoleBinding{
				ClusterRoleBinding: crb,
				RolePolicy: policyv1alpha1.RolePolicy{
					Rules: rules,
				},
			}
			am.Spec.AccessClusterRoleBinding = append(am.Spec.AccessClusterRoleBinding, accessClusterRoleBinding)
		}
	}

	if len(namespace) > 0 {
		var roleBindingList = &rbacv1.RoleBindingList{}
		if err := c.Client.List(ctx, roleBindingList, &client.ListOptions{Namespace: namespace}); err != nil {
			klog.Errorf("failed to list rolebindings, %v", err)
			return
		} else {
			for _, roleBinding := range roleBindingList.Items {
				_, applies := appliesTo(user, roleBinding.Subjects, namespace)
				if !applies {
					continue
				}
				rules, err := c.GetRoleReferenceRules(ctx, roleBinding.RoleRef, namespace)
				if err != nil {
					klog.Errorf("failed to get rules for rolebinding %s, %v", roleBinding.Name, err)
					return
				}
				var accessRoleBinding = policyv1alpha1.AccessRoleBinding{
					RoleBinding: roleBinding,
					RolePolicy: policyv1alpha1.RolePolicy{
						Rules: rules,
					},
				}
				am.Spec.AccessRoleBinding = append(am.Spec.AccessRoleBinding, accessRoleBinding)
			}
		}
	}
}
