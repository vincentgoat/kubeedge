package metamanager

import (
	"fmt"
	"github.com/astaxie/beego/orm"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/kubeedge/beehive/pkg/core"
	beehiveContext "github.com/kubeedge/beehive/pkg/core/context"
	"github.com/kubeedge/kubeedge/edge/pkg/common/modules"
	metamanagerconfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/constants"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao"
	v2 "github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao/v2"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver"
	metaserverconfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver/kubernetes/storage/sqlite/imitator"
	"github.com/kubeedge/kubeedge/pkg/apis/componentconfig/edgecore/v1alpha2"
)

type metaManager struct {
	enable  bool
	indexer cache.Indexer
}

var _ core.Module = (*metaManager)(nil)

func indexKeyFunc(obj interface{}) (string, error) {
	if tr, ok := obj.(*authenticationv1.TokenRequest); ok {
		return KeyFunc(tr.Name, tr.Namespace, tr), nil
	} else {
		return cache.MetaNamespaceKeyFunc(obj)
	}
}

func newMetaManager(enable bool) *metaManager {
	indexer := cache.NewIndexer(indexKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	err := indexer.AddIndexers(cache.Indexers{
		constants.TokenRequestIndexer: func(obj interface{}) ([]string, error) {
			if tr, ok := obj.(*authenticationv1.TokenRequest); !ok {
				return nil, fmt.Errorf("object is not *authenticationv1.TokenRequest")
			} else {
				return []string{tr.Status.Token}, nil
			}
		},
	})
	if err != nil {
		klog.Fatalf("add indexer for token request failed: %v", err)
	}
	return &metaManager{
		enable:  enable,
		indexer: indexer,
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
		go metaserver.NewMetaServer().Start(beehiveContext.Done())
	}

	m.runMetaManager()
}
