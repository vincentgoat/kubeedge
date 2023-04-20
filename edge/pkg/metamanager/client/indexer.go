package client

import (
	"fmt"
	"reflect"
	"sync"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/apis/rbac"
	rbacv1helpers "k8s.io/kubernetes/pkg/apis/rbac/v1"

	"github.com/kubeedge/beehive/pkg/core/model"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/constants"
	policyv1alpha1 "github.com/kubeedge/kubeedge/pkg/apis/policy/v1alpha1"
)

type itemsInPolicy struct {
	itemKey    map[string]interface{}
	ret        []interface{}
	targetKind string
}

func convertV1Rules(rules []rbac.PolicyRule) []rbacv1.PolicyRule {
	var convertedRules []rbacv1.PolicyRule
	for _, rule := range rules {
		var convertedRule rbacv1.PolicyRule
		if err := rbacv1helpers.Convert_rbac_PolicyRule_To_v1_PolicyRule(&rule, &convertedRule, nil); err != nil {
			klog.Errorf("failed to convert policy v1rule, %v", err)
			return nil
		}
		convertedRules = append(convertedRules, convertedRule)
	}
	return convertedRules
}

func (r itemsInPolicy) getRoleItems(m interface{}) {
	am, ok := m.(*policyv1alpha1.ServiceAccountAccess)
	if !ok {
		return
	}
	for _, rb := range am.Spec.AccessRoleBinding {
		if r.targetKind != "" && r.targetKind != rb.RoleBinding.RoleRef.Kind {
			continue
		}
		switch r.targetKind {
		case "Role":
			var tmpRole = &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rb.RoleBinding.RoleRef.Name,
					Namespace: am.Namespace,
				},
				Rules: convertV1Rules(rb.Rules),
			}
			key, err := cache.MetaNamespaceKeyFunc(tmpRole)
			if err != nil {
				continue
			}
			if _, ok := r.itemKey[key]; ok {
				continue
			}
			r.itemKey[key] = tmpRole
			r.ret = append(r.ret, tmpRole)

		case "ClusterRole":
			var tmpClusterRole = &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: rb.RoleBinding.RoleRef.Name,
				},
				Rules: convertV1Rules(rb.Rules),
			}
			key, err := cache.MetaNamespaceKeyFunc(tmpClusterRole)
			if err != nil {
				continue
			}
			if _, ok := r.itemKey[key]; ok {
				continue
			}
			r.itemKey[key] = tmpClusterRole
			r.ret = append(r.ret, tmpClusterRole)
		}
	}
	for _, crb := range am.Spec.AccessClusterRoleBinding {
		if r.targetKind != "" && r.targetKind != crb.ClusterRoleBinding.RoleRef.Kind {
			continue
		}
		var tmpClusterRole = &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: crb.ClusterRoleBinding.RoleRef.Name,
			},
			Rules: convertV1Rules(crb.Rules),
		}
		key, err := cache.MetaNamespaceKeyFunc(tmpClusterRole)
		if err != nil {
			continue
		}
		if _, ok := r.itemKey[key]; ok {
			continue
		}
		r.itemKey[key] = tmpClusterRole
		r.ret = append(r.ret, tmpClusterRole)
	}
}

func CacheServiceAccount(saIndexer cache.Indexer, sa *corev1.ServiceAccount, opr string) error {
	switch opr {
	case model.InsertOperation, model.UpdateOperation, model.PatchOperation:
		if _, exist, err := saIndexer.Get(sa); err == nil && exist {
			if err := saIndexer.Update(sa); err != nil {
				klog.Errorf("update cache serviceaccount failed: %v", err)
				return fmt.Errorf("update cache serviceaccount failed: %v", err)
			}
		} else {
			if err := saIndexer.Add(sa); err != nil {
				klog.Errorf("add cache serviceaccount failed: %v", err)
				return fmt.Errorf("add cache serviceaccount failed: %v", err)
			}
		}
	case model.DeleteOperation:
		if err := saIndexer.Delete(sa); err != nil {
			klog.Warningf("delete cache serviceaccount failed: %v", err)
		}
	}
	return nil
}

func CacheRole(roleIndexer cache.Indexer, acc *policyv1alpha1.ServiceAccountAccess, opr string) error {
	var roleFilter = itemsInPolicy{
		itemKey:    make(map[string]interface{}),
		targetKind: "Role",
	}
	roleFilter.getRoleItems(acc)
	for _, r := range roleFilter.ret {
		role := r.(*rbacv1.Role)
		switch opr {
		case model.InsertOperation, model.UpdateOperation, model.PatchOperation:
			if _, exist, err := roleIndexer.Get(role); err == nil && exist {
				if err := roleIndexer.Update(role); err != nil {
					klog.Errorf("update cache role failed: %v", err)
					return fmt.Errorf("update cache role failed: %v", err)
				}
			} else {
				if err := roleIndexer.Add(role); err != nil {
					klog.Errorf("add cache role failed: %v", err)
					return fmt.Errorf("add cache role failed: %v", err)
				}
			}
		case model.DeleteOperation:
			if err := roleIndexer.Delete(role); err != nil {
				klog.Warningf("delete cache role failed: %v", err)
				continue
			}
		}
	}
	return nil
}

func CacheClusterRole(crIndexer cache.Indexer, acc *policyv1alpha1.ServiceAccountAccess, opr string) error {
	var crFilter = itemsInPolicy{
		itemKey:    make(map[string]interface{}),
		targetKind: "ClusterRole",
	}
	crFilter.getRoleItems(acc)
	for _, r := range crFilter.ret {
		cr := r.(*rbacv1.ClusterRole)
		switch opr {
		case model.InsertOperation, model.UpdateOperation, model.PatchOperation:
			if _, exist, err := crIndexer.Get(cr); err == nil && exist {
				if err := crIndexer.Update(cr); err != nil {
					klog.Errorf("update cache clusterrole failed: %v", err)
					return fmt.Errorf("update cache clusterrole failed: %v", err)
				}
			} else {
				if err := crIndexer.Add(cr); err != nil {
					klog.Errorf("add cache clusterrole failed: %v", err)
					return fmt.Errorf("add cache clusterrole failed: %v", err)
				}
			}
		case model.DeleteOperation:
			if err := crIndexer.Delete(cr); err != nil {
				klog.Warningf("delete cache clusterrole failed: %v", err)
				continue
			}
		}
	}
	return nil
}

func CacheRoleBinding(rbIndexer cache.Indexer, acc *policyv1alpha1.ServiceAccountAccess, opr string) error {
	for _, rb := range acc.Spec.AccessRoleBinding {
		switch opr {
		case model.InsertOperation, model.UpdateOperation, model.PatchOperation:
			if _, exist, err := rbIndexer.Get(&rb.RoleBinding); err == nil && exist {
				if err := rbIndexer.Update(&rb.RoleBinding); err != nil {
					klog.Errorf("update cache rolebinding failed: %v", err)
					return fmt.Errorf("update cache rolebinding failed: %v", err)
				}
			} else {
				if err := rbIndexer.Add(&rb.RoleBinding); err != nil {
					klog.Errorf("add cache rolebinding failed: %v", err)
					return fmt.Errorf("add cache rolebinding failed: %v", err)
				}
			}
		case model.DeleteOperation:
			if err := rbIndexer.Delete(&rb.RoleBinding); err != nil {
				klog.Warningf("delete cache rolebinding failed: %v", err)
				continue
			}
		}
	}
	return nil
}

func CacheClusterRoleBinding(crbIndexer cache.Indexer, acc *policyv1alpha1.ServiceAccountAccess, opr string) error {
	for _, crb := range acc.Spec.AccessClusterRoleBinding {
		switch opr {
		case model.InsertOperation, model.UpdateOperation, model.PatchOperation:
			if _, exist, err := crbIndexer.Get(&crb.ClusterRoleBinding); err == nil && exist {
				if err := crbIndexer.Update(&crb.ClusterRoleBinding); err != nil {
					klog.Errorf("update cache clusterrolebinding failed: %v", err)
					return fmt.Errorf("update cache clusterrolebinding failed: %v", err)
				}
			} else {
				if err := crbIndexer.Add(&crb.ClusterRoleBinding); err != nil {
					klog.Errorf("add cache clusterrolebinding failed: %v", err)
					return fmt.Errorf("add cache clusterrolebinding failed: %v", err)
				}
			}
		case model.DeleteOperation:
			if err := crbIndexer.Delete(&crb.ClusterRoleBinding); err != nil {
				klog.Warningf("delete cache clusterrolebinding failed: %v", err)
				continue
			}
		}
	}
	return nil
}

type CacheManager struct {
	Indexers map[reflect.Type]cache.Indexer
	mux      sync.RWMutex
}

// KeyFunc keys should be nonconfidential and safe to log
func KeyFunc(name, namespace string, tr *authenticationv1.TokenRequest) string {
	var exp int64
	if tr.Spec.ExpirationSeconds != nil {
		exp = *tr.Spec.ExpirationSeconds
	}

	var ref authenticationv1.BoundObjectReference
	if tr.Spec.BoundObjectRef != nil {
		ref = *tr.Spec.BoundObjectRef
	}

	return fmt.Sprintf("%q/%q/%#v/%#v/%#v", name, namespace, tr.Spec.Audiences, exp, ref)
}

func indexKeyFunc(obj interface{}) (string, error) {
	if tr, ok := obj.(*authenticationv1.TokenRequest); ok {
		return KeyFunc(tr.Name, tr.Namespace, tr), nil
	} else {
		return cache.MetaNamespaceKeyFunc(obj)
	}
}

func (c *CacheManager) GetObjIndexer(obj runtime.Object) (cache.Indexer, error) {
	c.mux.RLock()
	defer c.mux.RUnlock()
	informerType := reflect.TypeOf(obj)
	if _, ok := c.Indexers[informerType]; ok {
		return c.Indexers[informerType], nil
	}
	return nil, fmt.Errorf("indexer not found")
}

func newIndexer(obj interface{}) cache.Indexer {
	indexer := cache.NewIndexer(indexKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	if _, ok := obj.(*authenticationv1.TokenRequest); !ok {
		return indexer
	}
	err := indexer.AddIndexers(cache.Indexers{
		constants.TokenRequestIndexer: func(obj interface{}) ([]string, error) {
			if tr, ok := obj.(*authenticationv1.TokenRequest); !ok {
				return []string{}, nil
			} else {
				return []string{tr.Status.Token}, nil
			}
		},
	})
	if err != nil {
		klog.Fatalf("add Indexers for token request failed: %v", err)
	}
	return indexer
}

func (c *CacheManager) NewIndexer(obj runtime.Object) cache.Indexer {
	c.mux.Lock()
	defer c.mux.Unlock()
	informerType := reflect.TypeOf(obj)
	c.Indexers[informerType] = newIndexer(obj)
	return c.Indexers[informerType]
}

func GetOrCreateIndexer(cache *CacheManager, obj runtime.Object) cache.Indexer {
	if indexer, err := cache.GetObjIndexer(obj); err != nil {
		return cache.NewIndexer(obj)
	} else {
		return indexer
	}
}
