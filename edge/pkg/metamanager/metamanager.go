package metamanager

import (
	"reflect"

	"github.com/astaxie/beego/orm"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubeedge/beehive/pkg/core"
	beehiveContext "github.com/kubeedge/beehive/pkg/core/context"
	"github.com/kubeedge/kubeedge/edge/pkg/common/modules"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/client"
	metamanagerconfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao"
	v2 "github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao/v2"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver"
	metaserverconfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver/kubernetes/storage/sqlite/imitator"
	"github.com/kubeedge/kubeedge/pkg/apis/componentconfig/edgecore/v1alpha2"
	policyv1alpha1 "github.com/kubeedge/kubeedge/pkg/apis/policy/v1alpha1"
)

type metaManager struct {
	enable bool
	Cache  *client.CacheManager
}

var _ core.Module = (*metaManager)(nil)

func (m *metaManager) getIndexer(obj runtime.Object) cache.Indexer {
	return client.GetOrNewIndexer(m.Cache, obj)
}

func (m *metaManager) cachePolicyResource(am *policyv1alpha1.AccessMixer, opr string) error {
	if err := client.CacheServiceAccount(m.getIndexer(&am.Spec.ServiceAccount), &am.Spec.ServiceAccount, opr); err != nil {
		return err
	}
	if err := client.CacheRole(m.getIndexer(&rbacv1.Role{}), am, opr); err != nil {
		return err
	}
	if err := client.CacheClusterRole(m.getIndexer(&rbacv1.ClusterRole{}), am, opr); err != nil {
		return err
	}
	if err := client.CacheRoleBinding(m.getIndexer(&rbacv1.RoleBinding{}), am, opr); err != nil {
		return err
	}
	if err := client.CacheClusterRoleBinding(m.getIndexer(&rbacv1.ClusterRoleBinding{}), am, opr); err != nil {
		return err
	}
	return nil
}

func newMetaManager(enable bool) *metaManager {
	return &metaManager{
		enable: enable,
		Cache:  &client.CacheManager{Indexers: make(map[reflect.Type]cache.Indexer)},
	}
}

// Register register metamanager
func Register(metaManager *v1alpha2.MetaManager) {
	metamanagerconfig.InitConfigure(metaManager)
	meta := newMetaManager(metaManager.Enable)
	initDBTable(meta)
	core.Register(meta)
}

// initDBTable create table
func initDBTable(module core.Module) {
	klog.Infof("Begin to register %v db model", module.Name())
	if !module.Enable() {
		klog.Infof("Module %s is disabled, DB meta for it will not be registered", module.Name())
		return
	}
	orm.RegisterModel(new(dao.Meta))
	orm.RegisterModel(new(v2.MetaV2))
}

func (*metaManager) Name() string {
	return modules.MetaManagerModuleName
}

func (*metaManager) Group() string {
	return modules.MetaGroup
}

func (m *metaManager) Enable() bool {
	return m.enable
}

func (m *metaManager) Start() {
	if metaserverconfig.Config.Enable {
		imitator.StorageInit()
		go metaserver.NewMetaServer(m.Cache).Start(beehiveContext.Done())
	}

	m.runMetaManager()
}
