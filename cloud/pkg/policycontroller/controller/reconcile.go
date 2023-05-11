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
	"k8s.io/kubernetes/pkg/apis/rbac"
	rbacv1helpers "k8s.io/kubernetes/pkg/apis/rbac/v1"
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
	acc := &policyv1alpha1.ServiceAccountAccess{}
	if err := c.Client.Get(ctx, request.NamespacedName, acc); err != nil {
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}
		klog.Errorf("failed to get serviceaccountaccess %s/%s, %v", request.Namespace, request.Name, err)
		return controllerruntime.Result{Requeue: true}, err
	}

	if !acc.GetDeletionTimestamp().IsZero() {
		return controllerruntime.Result{}, nil
	}

	return c.syncRules(ctx, acc)
}

func (c *Controller) filterResource(ctx context.Context, object client.Object) bool {
	accList := &policyv1alpha1.ServiceAccountAccessList{}
	if err := c.Client.List(ctx, accList); err != nil {
		klog.Errorf("failed to list serviceaccountaccess, %v", err)
		return false
	}
	var p = &PolicyMatcher{}
	matchTarget(accList, object, p.Visitor)
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

func (pm *PolicyMatcher) Visitor(binding interface{}, acc *policyv1alpha1.ServiceAccountAccess, object client.Object) bool {
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

func (p *PolicyRequestVisitor) Visitor(binding interface{}, acc *policyv1alpha1.ServiceAccountAccess, object client.Object) bool {
	switch object.GetObjectKind().GroupVersionKind().Kind {
	case "ClusterRole", "Role":
		switch binding.(type) {
		case *rbacv1.ClusterRoleBinding:
			if isMatchedRoleRef(binding.(*rbacv1.ClusterRoleBinding).RoleRef, "", object) {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: acc.Namespace, Name: acc.Name}})
			}

		case *rbacv1.RoleBinding:
			if isMatchedRoleRef(binding.(*rbacv1.RoleBinding).RoleRef, binding.(*rbacv1.RoleBinding).Namespace, object) {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: acc.Namespace, Name: acc.Name}})
			}
		}

	case "ClusterRoleBinding", "RoleBinding":
		switch binding.(type) {
		case *rbacv1.ClusterRoleBinding:
			if binding.(*rbacv1.ClusterRoleBinding).Name == object.GetName() {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: acc.Namespace, Name: acc.Name}})
			}
		case *rbacv1.RoleBinding:
			if binding.(*rbacv1.RoleBinding).Name == object.GetName() && binding.(*rbacv1.RoleBinding).Namespace == object.GetNamespace() {
				p.AuthPolicy = append(p.AuthPolicy, controllerruntime.Request{NamespacedName: client.ObjectKey{Namespace: acc.Namespace, Name: acc.Name}})
			}
		}
	}

	return true
}

func matchTarget(accList *policyv1alpha1.ServiceAccountAccessList, object client.Object, visitor func(interface{}, *policyv1alpha1.ServiceAccountAccess, client.Object) bool) {
	for _, am := range accList.Items {
		for _, binding := range am.Spec.AccessClusterRoleBinding {
			if !visitor(&binding.ClusterRoleBinding, &am, object) {
				return
			}
		}
		for _, binding := range am.Spec.AccessRoleBinding {
			if !visitor(&binding.RoleBinding, &am, object) {
				return
			}
		}
	}
}

func (c *Controller) mapRolesFunc(object client.Object) []controllerruntime.Request {
	accList := &policyv1alpha1.ServiceAccountAccessList{}
	if err := c.Client.List(context.Background(), accList); err != nil {
		klog.Errorf("failed to list serviceaccountaccess, %v", err)
		return nil
	}
	var p = PolicyRequestVisitor{}
	matchTarget(accList, object, p.Visitor)
	return p.AuthPolicy
}

func newSaAccessObject(sa corev1.ServiceAccount) *policyv1alpha1.ServiceAccountAccess {
	return &policyv1alpha1.ServiceAccountAccess{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sa.GetName(),
			Namespace: sa.GetNamespace(),
		},
		Spec: policyv1alpha1.AccessSpec{
			ServiceAccount: corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sa.GetName(),
					Namespace: sa.GetNamespace(),
				},
			},
		},
	}
}

func (c *Controller) mapObjectFunc(object client.Object) []controllerruntime.Request {
	accList := &policyv1alpha1.ServiceAccountAccessList{}
	if err := c.Client.List(context.Background(), accList, &client.ListOptions{Namespace: object.GetNamespace()}); err != nil {
		klog.Errorf("failed to list serviceaccountaccess, %v", err)
		return nil
	}
	switch object.(type) {
	case *corev1.Pod:
		sa := object.(*corev1.Pod).Spec.ServiceAccountName
		for _, am := range accList.Items {
			if am.Spec.ServiceAccount.Name == sa && am.Spec.ServiceAccount.Namespace == object.GetNamespace() {
				return []controllerruntime.Request{{NamespacedName: client.ObjectKey{Namespace: am.Namespace, Name: am.Name}}}
			}
		}
		// create serviceaccountaccess if not exist when pod event triggered
		if err := c.Client.Create(context.Background(), newSaAccessObject(corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sa,
				Namespace: object.GetNamespace(),
			},
		})); err != nil {
			klog.Errorf("failed to create serviceaccountaccess, %v", err)
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
		accList := &policyv1alpha1.ServiceAccountAccessList{}
		if err := c.Client.List(ctx, accList, &client.ListOptions{Namespace: object.GetNamespace()}); err != nil {
			klog.Errorf("failed to list serviceaccountaccess, %v", err)
			return false
		}
		for _, am := range accList.Items {
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
		For(&policyv1alpha1.ServiceAccountAccess{}).
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

func getNodeListOfServiceAccountAccess(ctx context.Context, cli client.Client, acc *policyv1alpha1.ServiceAccountAccess) ([]string, error) {
	var nodeList []string
	podList := &corev1.PodList{}
	saNameSelector := fields.OneTermEqualSelector("spec.serviceAccountName", acc.Spec.ServiceAccount.Name)
	allSelectors := fields.AndSelectors(saNameSelector, fields.OneTermEqualSelector("metadata.namespace", acc.Namespace))
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
	var intersect = []string{}
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
	var subtract = []string{}
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

func (c *Controller) send2Edge(acc *policyv1alpha1.ServiceAccountAccess, targets []string, opr string) {
	sendObj := *acc.DeepCopy()
	for _, node := range targets {
		resource, err := messagelayer.BuildResource(node, sendObj.Namespace, model.ResourceTypeAccessRoleMixer, sendObj.Name)
		if err != nil {
			klog.Warningf("built message resource failed with error: %s", err)
			continue
		}
		// filter out the node list data
		sendObj.Status.NodeList = []string{}
		msg := model.NewMessage("").
			SetResourceVersion(sendObj.ResourceVersion).
			FillBody(sendObj).BuildRouter(modules.PolicyControllerModuleName, constants.GroupResource, resource, opr)
		if err := c.MessageLayer.Send(*msg); err != nil {
			klog.Warningf("send message %s failed with error: %s", resource, err)
			continue
		}
	}
	return
}

func (c *Controller) syncRules(ctx context.Context, acc *policyv1alpha1.ServiceAccountAccess) (controllerruntime.Result, error) {
	var newSA corev1.ServiceAccount
	err := c.Client.Get(ctx, types.NamespacedName{Namespace: acc.Namespace, Name: acc.Spec.ServiceAccount.Name}, &newSA)
	if err != nil && apierrors.IsNotFound(err) {
		klog.V(4).Infof("serviceaccount %s/%s not found and delete the policy resource", acc.Namespace, acc.Spec.ServiceAccount.Name)
		copyObj := acc.DeepCopy()
		if err := c.Client.Delete(ctx, copyObj); err != nil {
			klog.Errorf("failed to delete serviceaccountaccess %s/%s, %v", copyObj.Namespace, copyObj.Name, err)
			return controllerruntime.Result{Requeue: true}, err
		}
		c.send2Edge(copyObj, copyObj.Status.NodeList, model.DeleteOperation)
		return controllerruntime.Result{}, nil
	} else if err != nil {
		klog.Errorf("failed to get serviceaccount %s/%s, %v", acc.Namespace, acc.Spec.ServiceAccount.Name, err)
		return controllerruntime.Result{Requeue: true}, err
	}
	var newAcc = *acc.DeepCopy()
	newAcc.Spec = policyv1alpha1.AccessSpec{}
	newAcc.Spec.ServiceAccount = *newSA.DeepCopy()
	userInfo := serviceaccount.UserInfo(newSA.Namespace, newSA.Name, string(newSA.UID))
	c.VisitRulesFor(ctx, userInfo, acc.Namespace, &newAcc)
	nodes, err := getNodeListOfServiceAccountAccess(ctx, c.Client, acc)
	if err != nil {
		klog.Errorf("failed to get node list of serviceaccountaccess %s/%s, %v", acc.Namespace, acc.Name, err)
		return controllerruntime.Result{Requeue: true}, err
	}
	if len(nodes) == 0 && len(acc.Status.NodeList) == 0 {
		klog.Warningf("no nodes found for serviceaccountaccess %s/%s", acc.Namespace, acc.Name)
		return controllerruntime.Result{}, nil
	}
	deleteNodes := subtractSlice(nodes, acc.Status.NodeList)
	if len(deleteNodes) != 0 {
		// no nodes in the current acc status, delete the acc
		if len(nodes) == 0 {
			if err = c.Client.Delete(ctx, acc); err != nil {
				klog.Errorf("failed to delete serviceaccountaccess %s/%s, %v", acc.Namespace, acc.Name, err)
				return controllerruntime.Result{Requeue: true}, err
			}
			klog.V(4).Infof("delete serviceaccountaccess %s/%s", acc.Namespace, acc.Name)
			c.send2Edge(acc, deleteNodes, model.DeleteOperation)
			return controllerruntime.Result{}, nil
		}
		c.send2Edge(acc, deleteNodes, model.DeleteOperation)
	}
	newAcc.Status.NodeList = append([]string{}, nodes...)
	sort.Slice(acc.Spec.AccessRoleBinding, func(i, j int) bool {
		return acc.Spec.AccessRoleBinding[i].RoleBinding.Name < acc.Spec.AccessRoleBinding[j].RoleBinding.Name
	})
	sort.Slice(newAcc.Spec.AccessRoleBinding, func(i, j int) bool {
		return newAcc.Spec.AccessRoleBinding[i].RoleBinding.Name < newAcc.Spec.AccessRoleBinding[j].RoleBinding.Name
	})
	sort.Slice(acc.Spec.AccessClusterRoleBinding, func(i, j int) bool {
		return acc.Spec.AccessClusterRoleBinding[i].ClusterRoleBinding.Name < acc.Spec.AccessClusterRoleBinding[j].ClusterRoleBinding.Name
	})
	sort.Slice(newAcc.Spec.AccessClusterRoleBinding, func(i, j int) bool {
		return newAcc.Spec.AccessClusterRoleBinding[i].ClusterRoleBinding.Name < newAcc.Spec.AccessClusterRoleBinding[j].ClusterRoleBinding.Name
	})
	if !equality.Semantic.DeepEqual(acc.Spec.AccessClusterRoleBinding, newAcc.Spec.AccessClusterRoleBinding) ||
		!equality.Semantic.DeepEqual(acc.Spec.AccessRoleBinding, newAcc.Spec.AccessRoleBinding) ||
		!equality.Semantic.DeepEqual(acc.Spec.ServiceAccount, newAcc.Spec.ServiceAccount) {
		if err := c.Client.Patch(ctx, &newAcc, client.MergeFrom(acc)); err != nil {
			klog.Errorf("failed to patch serviceaccountaccess %s/%s, %v", acc.Namespace, acc.Name, err)
			return controllerruntime.Result{Requeue: true}, err
		}
		acc.Spec = *(newAcc.Spec.DeepCopy())
		c.send2Edge(acc, nodes, model.UpdateOperation)
	} else {
		addNodes := subtractSlice(acc.Status.NodeList, nodes)
		klog.V(4).Infof("serviceaccountaccess spec %s/%s is up to date", acc.Namespace, acc.Name)
		if len(addNodes) != 0 {
			if err := c.Client.Patch(ctx, &newAcc, client.MergeFrom(acc)); err != nil {
				klog.Errorf("failed to patch serviceaccountaccess %s/%s, %v", acc.Namespace, acc.Name, err)
				return controllerruntime.Result{Requeue: true}, err
			}
			c.send2Edge(acc, addNodes, model.InsertOperation)
		}
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

func convertRules(rules []rbacv1.PolicyRule) []rbac.PolicyRule {
	var convertedRules []rbac.PolicyRule
	for _, rule := range rules {
		var convertedRule rbac.PolicyRule
		if err := rbacv1helpers.Convert_v1_PolicyRule_To_rbac_PolicyRule(&rule, &convertedRule, nil); err != nil {
			klog.Errorf("failed to convert policyrule, %v", err)
			return nil
		}
		convertedRules = append(convertedRules, convertedRule)
	}
	return convertedRules
}

func (c *Controller) VisitRulesFor(ctx context.Context, user user.Info, namespace string, acc *policyv1alpha1.ServiceAccountAccess) {
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
			conventCrb := &rbac.ClusterRoleBinding{}
			var accessClusterRoleBinding = policyv1alpha1.AccessClusterRoleBinding{
				ClusterRoleBinding: *conventCrb,
				Rules:              convertRules(rules),
			}
			acc.Spec.AccessClusterRoleBinding = append(acc.Spec.AccessClusterRoleBinding, accessClusterRoleBinding)
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
				var convertRb = &rbac.RoleBinding{}
				if err := rbacv1helpers.Convert_v1_RoleBinding_To_rbac_RoleBinding(&roleBinding, convertRb, nil); err != nil {
					klog.Errorf("failed to convert rolebinding, %v", err)
					return
				}
				var accessRoleBinding = policyv1alpha1.AccessRoleBinding{
					RoleBinding: *convertRb,
					Rules:       convertRules(rules),
				}
				acc.Spec.AccessRoleBinding = append(acc.Spec.AccessRoleBinding, accessRoleBinding)
			}
		}
	}
}
