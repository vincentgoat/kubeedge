/*
Copyright 2018 The KubeEdge Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metamanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	coordinationv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubeedge/beehive/pkg/common"
	"github.com/kubeedge/beehive/pkg/core"
	beehiveContext "github.com/kubeedge/beehive/pkg/core/context"
	"github.com/kubeedge/beehive/pkg/core/model"
	"github.com/kubeedge/kubeedge/common/constants"
	"github.com/kubeedge/kubeedge/edge/mocks/beego"
	connect "github.com/kubeedge/kubeedge/edge/pkg/common/cloudconnection"
	"github.com/kubeedge/kubeedge/edge/pkg/common/dbm"
	"github.com/kubeedge/kubeedge/edge/pkg/common/modules"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/client"
	metaManagerConfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao"
	"github.com/kubeedge/kubeedge/pkg/apis/componentconfig/edgecore/v1alpha2"
)

const (
	MessageTest = "Test"
	// FailedDBOperation is common Database operation fail message
	FailedDBOperation = "Failed DB Operation"
	// ModuleNameEdged is name of edged module
	ModuleNameEdged = "edged"
	// ModuleNameEdgeHub is name of edgehub module
	ModuleNameEdgeHub = "websocket"
	// ModuleNameController is the name of the controller module
	ModuleNameController = "edgecontroller"
	// OperationNodeConnection is message with operation publish
	OperationNodeConnection = "publish"
)

// errFailedDBOperation is common Database operation fail error
var errFailedDBOperation = errors.New(FailedDBOperation)

func init() {
	cfg := v1alpha2.NewDefaultEdgeCoreConfig()
	metaManagerConfig.InitConfigure(cfg.Modules.MetaManager)

	beehiveContext.InitContext([]string{common.MsgCtxTypeChannel})
	add := &common.ModuleInfo{
		ModuleName: modules.MetaManagerModuleName,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(add)
}

// TestProcessInsert is function to test processInsert
func TestProcessInsert(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	ormerMock := beego.NewMockOrmer(mockCtrl)
	rawSetterMock := beego.NewMockRawSeter(mockCtrl)
	dbm.DBAccess = ormerMock
	meta := newMetaManager(true)
	core.Register(meta)

	add := &common.ModuleInfo{
		ModuleName: meta.Name(),
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(add)
	beehiveContext.AddModuleGroup(meta.Name(), meta.Group())
	edgeHub := &common.ModuleInfo{
		ModuleName: ModuleNameEdgeHub,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edgeHub)
	beehiveContext.AddModuleGroup(ModuleNameEdgeHub, modules.HubGroup)
	edged := &common.ModuleInfo{
		ModuleName: ModuleNameEdged,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edged)

	//SaveMeta Failed, feedbackError SendToCloud
	rawSetterMock.EXPECT().Exec().Return(nil, errFailedDBOperation).Times(1)
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)

	msg := model.NewMessage("").BuildRouter(modules.MetaManagerModuleName, GroupResource, model.ResourceTypePodStatus, model.InsertOperation)
	meta.processInsert(*msg)
	//beehiveContext.Send(MetaManagerModuleName, *msg)
	message, err := beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("EdgeHubChannelRegistration", func(t *testing.T) {
		if err != nil {
			t.Errorf("EdgeHub Channel not found: %v", err)
			return
		}
		want := "insert or update meta failed, " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//SaveMeta Failed, feedbackError SendToEdged and 2 resources
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, errFailedDBOperation).Times(1)

	resource := fmt.Sprintf("%s/%s/%s", "default", model.ResourceTypeLease, "leaseName")
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, resource, model.InsertOperation)
	meta.processInsert(*msg)
	message, err = beehiveContext.Receive(ModuleNameEdgeHub)
	if err != nil {
		t.Errorf("EdgeHub Channel not found: %v", err)
		return
	}
	message.Header.ParentID = message.GetID()
	message.Content = &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "leaseName",
			Namespace: "default",
		},
	}
	beehiveContext.SendResp(message)
	message, err = beehiveContext.Receive(ModuleNameEdged)
	t.Run("InsertCache", func(t *testing.T) {
		if err != nil {
			t.Errorf("EdgeD Channel not found: %v", err)
			return
		}
		want := "insert or update meta failed, " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
		indexer, err := meta.Cache.GetObjIndexer(&coordinationv1.Lease{})
		if err != nil {
			t.Errorf("Error getting indexer: %v", err)
			return
		}
		_, exists, err := indexer.GetByKey("default/leaseName")
		if err != nil || !exists {
			t.Errorf("Error getting lease from indexer: %v", err)
			return
		}
	})

	//jsonMarshall fail
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, model.ResourceTypePodStatus, model.InsertOperation).FillBody(make(chan int))
	meta.processInsert(*msg)
	message, err = beehiveContext.Receive(ModuleNameEdgeHub)
	message.Header.ParentID = message.GetID()
	beehiveContext.SendResp(message)
	message, err = beehiveContext.Receive(ModuleNameEdged)
	t.Run("MarshallFail", func(t *testing.T) {
		want := "get message content data failed, error: marshal message content failed: json: unsupported type: chan int"
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//Successful Case and 3 resources
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, nil).Times(1)

	msg = model.NewMessage("").BuildRouter(ModuleNameController, GroupResource, model.ResourceTypePodStatus+"/secondRes"+"/thirdRes", model.InsertOperation)
	meta.processInsert(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("InsertMessageToEdged", func(t *testing.T) {
		want := model.InsertOperation
		if message.GetOperation() != want {
			t.Errorf("Wrong message received : Wanted %v and Got %v", want, message.GetOperation())
		}
	})
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("ResponseMessageToEdgeHub", func(t *testing.T) {
		want := OK
		if message.GetContent() != want {
			t.Errorf("Wrong message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})
}

// TestProcessUpdate is function to test processUpdate
func TestProcessUpdate(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	ormerMock := beego.NewMockOrmer(mockCtrl)
	rawSetterMock := beego.NewMockRawSeter(mockCtrl)
	dbm.DBAccess = ormerMock
	meta := newMetaManager(true)
	core.Register(meta)

	add := &common.ModuleInfo{
		ModuleName: meta.Name(),
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(add)
	beehiveContext.AddModuleGroup(meta.Name(), meta.Group())
	edgeHub := &common.ModuleInfo{
		ModuleName: ModuleNameEdgeHub,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edgeHub)
	beehiveContext.AddModuleGroup(ModuleNameEdgeHub, modules.HubGroup)
	edged := &common.ModuleInfo{
		ModuleName: ModuleNameEdged,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edged)

	//jsonMarshall fail
	msg := model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, "namespace/"+model.ResourceTypePodStatus+"/id", model.UpdateOperation).FillBody(make(chan int))
	meta.processUpdate(*msg)
	message, _ := beehiveContext.Receive(ModuleNameEdgeHub)
	message.Header.ParentID = message.GetID()
	beehiveContext.SendResp(message)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("MarshallFail", func(t *testing.T) {
		want := "get message content data failed, error: marshal message content failed: json: unsupported type: chan int"
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//Database save error
	rawSetterMock.EXPECT().Exec().Return(nil, errFailedDBOperation).Times(1)
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, model.ResourceTypeNode, model.UpdateOperation)
	meta.processUpdate(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("DatabaseSaveError", func(t *testing.T) {
		want := "insert or update meta failed, " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//Success Case Source Edged, sync = true
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, nil).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, model.ResourceTypePodStatus, model.UpdateOperation)
	meta.processUpdate(*msg)
	edgehubMsg, _ := beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("SuccessSourceEdgedReceiveEdgehub", func(t *testing.T) {
		want := model.UpdateOperation
		if edgehubMsg.GetOperation() != want {
			t.Errorf("Wrong message received : Wanted operation %v and Got operation %v", want, edgehubMsg.GetOperation())
		}
	})
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("SuccessSourceEdgedReceiveEdged", func(t *testing.T) {
		want := OK
		if message.GetContent() != want {
			t.Errorf("Wrong message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//Success Case Source CloudControllerModel
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, nil).Times(1)
	msg = model.NewMessage("").BuildRouter(CloudControllerModel, GroupResource, model.ResourceTypePodStatus, model.UpdateOperation)
	meta.processUpdate(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("SuccessSend[CloudController->Edged]", func(t *testing.T) {
		want := CloudControllerModel
		if message.GetSource() != want {
			t.Errorf("Wrong message received : Wanted from source %v and Got from source %v", want, message.GetSource())
		}
	})
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("SuccessSendCloud[CloudController->EdgeHub]", func(t *testing.T) {
		want := OK
		if message.GetContent() != want {
			t.Errorf("Wrong message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	indexer := client.GetOrCreateIndexer(meta.Cache, &coordinationv1.Lease{})

	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "leaseName2",
			Namespace: "default",
		},
	}
	err := indexer.Add(lease)
	if err != nil {
		t.Errorf("Error add lease to indexer: %v", err)
		return
	}
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, errFailedDBOperation).Times(1)
	resource := fmt.Sprintf("%s/%s/%s", "default", model.ResourceTypeLease, "leaseName2")
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, resource, model.UpdateOperation)
	meta.processUpdate(*msg)
	message, err = beehiveContext.Receive(ModuleNameEdgeHub)
	if err != nil {
		t.Errorf("EdgeHub Channel not found: %v", err)
		return
	}
	message.Header.ParentID = message.GetID()
	holderIdentity := "edgehub"
	message.Content = &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "leaseName2",
			Namespace: "default",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &holderIdentity,
		},
	}
	beehiveContext.SendResp(message)
	message, err = beehiveContext.Receive(ModuleNameEdged)
	t.Run("UpdateCache", func(t *testing.T) {
		if err != nil {
			t.Errorf("EdgeD Channel not found: %v", err)
			return
		}
		want := "insert or update meta failed, " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
		indexer, err := meta.Cache.GetObjIndexer(&coordinationv1.Lease{})
		if err != nil {
			t.Errorf("Error getting indexer: %v", err)
			return
		}
		le, exists, err := indexer.GetByKey("default/leaseName2")
		if err != nil || !exists {
			t.Errorf("Error getting lease from indexer: %v", err)
			return
		}
		if le.(*coordinationv1.Lease).Spec.HolderIdentity == nil {
			t.Errorf("Error getting lease from indexer: %v", err)
			return
		}
		if le.(*coordinationv1.Lease).Spec.HolderIdentity != nil && *le.(*coordinationv1.Lease).Spec.HolderIdentity != "edgehub" {
			t.Errorf("Error getting lease from indexer: %v", err)
			return
		}
	})
}

// TestProcessResponse is function to test processResponse
func TestProcessResponse(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	ormerMock := beego.NewMockOrmer(mockCtrl)
	rawSetterMock := beego.NewMockRawSeter(mockCtrl)
	dbm.DBAccess = ormerMock
	meta := newMetaManager(true)
	core.Register(meta)

	add := &common.ModuleInfo{
		ModuleName: meta.Name(),
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(add)
	beehiveContext.AddModuleGroup(meta.Name(), meta.Group())
	edgeHub := &common.ModuleInfo{
		ModuleName: ModuleNameEdgeHub,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edgeHub)
	beehiveContext.AddModuleGroup(ModuleNameEdgeHub, modules.HubGroup)
	addEdged := &common.ModuleInfo{
		ModuleName: ModuleNameEdged,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(addEdged)

	//jsonMarshall fail
	msg := model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, model.ResourceTypePodStatus, model.ResponseOperation).FillBody(make(chan int))
	beehiveContext.Send(modules.MetaManagerModuleName, *msg)
	meta.processResponse(*msg)
	message, _ := beehiveContext.Receive(ModuleNameEdged)
	t.Run("MarshallFail", func(t *testing.T) {
		want := "get message content data failed, error: marshal message content failed: json: unsupported type: chan int"
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//Database save error
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, errFailedDBOperation).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, model.ResourceTypePodStatus, model.ResponseOperation)
	meta.processResponse(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("DatabaseSaveError", func(t *testing.T) {
		want := "insert or update meta failed, " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//Success Case Source EdgeD
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, nil).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, GroupResource, model.ResourceTypePodStatus, model.ResponseOperation)
	meta.processResponse(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("SuccessSourceEdged", func(t *testing.T) {
		want := ModuleNameEdged
		if message.GetSource() != want {
			t.Errorf("Wrong message received : Wanted from source %v and Got from source %v", want, message.GetSource())
		}
	})

	//Success Case Source EdgeHub
	ormerMock.EXPECT().Raw(gomock.Any(), gomock.Any()).Return(rawSetterMock).Times(1)
	rawSetterMock.EXPECT().Exec().Return(nil, nil).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameController, GroupResource, model.ResourceTypePodStatus, model.ResponseOperation)
	meta.processResponse(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("SuccessSourceEdgeHub", func(t *testing.T) {
		want := ModuleNameController
		if message.GetSource() != want {
			t.Errorf("Wrong message received : Wanted from source %v and Got from source %v", want, message.GetSource())
		}
	})
}

// TestProcessDelete is function to test processDelete
func TestProcessDelete(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	ormerMock := beego.NewMockOrmer(mockCtrl)
	querySetterMock := beego.NewMockQuerySeter(mockCtrl)
	dbm.DBAccess = ormerMock
	meta := newMetaManager(true)
	core.Register(meta)

	add := &common.ModuleInfo{
		ModuleName: meta.Name(),
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(add)
	beehiveContext.AddModuleGroup(meta.Name(), meta.Group())
	edgeHub := &common.ModuleInfo{
		ModuleName: ModuleNameEdgeHub,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edgeHub)
	beehiveContext.AddModuleGroup(ModuleNameEdgeHub, modules.HubGroup)
	edged := &common.ModuleInfo{
		ModuleName: ModuleNameEdged,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edged)

	//Database Save Error
	querySetterMock.EXPECT().Filter(gomock.Any(), gomock.Any()).Return(querySetterMock).Times(1)
	querySetterMock.EXPECT().Delete().Return(int64(1), errFailedDBOperation).Times(1)
	ormerMock.EXPECT().QueryTable(gomock.Any()).Return(querySetterMock).Times(1)
	msg := model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, model.ResourceTypePodStatus, model.DeleteOperation)
	meta.processDelete(*msg)
	message, _ := beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("DatabaseDeleteError", func(t *testing.T) {
		want := "delete meta failed, " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//Success Case
	querySetterMock.EXPECT().Filter(gomock.Any(), gomock.Any()).Return(querySetterMock).Times(1)
	querySetterMock.EXPECT().Delete().Return(int64(1), nil).Times(1)
	ormerMock.EXPECT().QueryTable(gomock.Any()).Return(querySetterMock).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, model.ResourceTypePodStatus, model.DeleteOperation)
	meta.processDelete(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("SuccessSourceEdgeHub", func(t *testing.T) {
		want := ModuleNameEdgeHub
		if message.GetSource() != want {
			t.Errorf("Wrong message received : Wanted from source %v and Got from source %v", want, message.GetSource())
		}
	})
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("SuccessResponseOK", func(t *testing.T) {
		want := OK
		if message.GetContent() != want {
			t.Errorf("Wrong message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	// Success Case
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "test",
			UID:       "1234567890",
		},
	}
	podBytes, _ := json.Marshal(pod)
	fakeDao := new([]dao.Meta)
	fakeDaoArray := make([]dao.Meta, 1)
	fakeDaoArray[0] = dao.Meta{Key: "Test", Value: string(podBytes)}
	fakeDao = &fakeDaoArray
	querySetterMock.EXPECT().Filter(gomock.Any(), gomock.Any()).Return(querySetterMock).Times(4)
	querySetterMock.EXPECT().All(gomock.Any()).SetArg(0, *fakeDao).Return(int64(1), nil).Times(1)
	querySetterMock.EXPECT().Delete().Return(int64(1), nil).Times(3)
	ormerMock.EXPECT().QueryTable(gomock.Any()).Return(querySetterMock).Times(4)
	indexer := client.GetOrCreateIndexer(meta.Cache, &v1.Pod{})

	err := indexer.Add(pod)
	if err != nil {
		t.Errorf("Error add lease to indexer: %v", err)
		return
	}
	resource := fmt.Sprintf("test/%s/nginx", model.ResourceTypePod)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdged, modules.MetaGroup, resource, model.DeleteOperation).FillBody(podBytes)
	meta.processDelete(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	message.Header.ParentID = message.GetID()
	message.Content = constants.MessageSuccessfulContent
	beehiveContext.SendResp(message)
	message, err = beehiveContext.Receive(ModuleNameEdged)
	t.Run("SuccessDeleteRemotePod", func(t *testing.T) {
		want := ModuleNameEdged
		if message.GetSource() != want {
			t.Errorf("Wrong message received : Wanted from source %v and Got from source %v", want, message.GetSource())
		}
		if _, exist, _ := indexer.GetByKey("test/nginx"); exist {
			t.Errorf("Pod should be deleted from indexer")
		}
	})
}

// TestProcessQuery is function to test processQuery
func TestProcessQuery(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	ormerMock := beego.NewMockOrmer(mockCtrl)
	querySetterMock := beego.NewMockQuerySeter(mockCtrl)
	dbm.DBAccess = ormerMock
	meta := newMetaManager(true)
	core.Register(meta)

	add := &common.ModuleInfo{
		ModuleName: meta.Name(),
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(add)
	beehiveContext.AddModuleGroup(meta.Name(), meta.Group())
	edgeHub := &common.ModuleInfo{
		ModuleName: ModuleNameEdgeHub,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edgeHub)
	beehiveContext.AddModuleGroup(ModuleNameEdgeHub, modules.HubGroup)
	edged := &common.ModuleInfo{
		ModuleName: ModuleNameEdged,
		ModuleType: common.MsgCtxTypeChannel,
	}
	beehiveContext.AddModule(edged)

	connect.SetConnected(true)

	//process remote query jsonMarshall error
	msg := model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeConfigmap, model.QueryOperation).FillBody(make(chan int))
	meta.processQuery(*msg)
	message, _ := beehiveContext.Receive(ModuleNameEdgeHub)
	message.Header.ParentID = message.GetID()
	message.SetResourceOperation("test/"+model.ResourceTypeConfigmap, model.ResponseOperation)
	beehiveContext.SendResp(message)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("ProcessRemoteQueryMarshallFail", func(t *testing.T) {
		want := "get message content data failed, error: marshal message content failed: json: unsupported type: chan int"
		if message.GetContent() != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//process remote query response error content
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeConfigmap, model.QueryOperation)
	meta.processQuery(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	msg = model.NewMessage(message.GetID()).BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeConfigmap, model.QueryOperation).FillBody(fmt.Errorf("test"))
	beehiveContext.SendResp(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	msgEdged, _ := beehiveContext.Receive(ModuleNameEdged)
	t.Run("ProcessRemoteQueryResponseErrorContent", func(t *testing.T) {
		want := OK
		if message.GetContent().(string) != want {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", want, message.GetContent())
		}
		wantEdged := fmt.Errorf("test").Error()
		if msgEdged.GetContent().(error).Error() != wantEdged {
			t.Errorf("Wrong Error message received : Wanted %v and Got %v", wantEdged, msgEdged.GetContent().(error).Error())
		}
	})

	//process remote query db fail
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeConfigmap, model.QueryOperation)
	meta.processQuery(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	msg = model.NewMessage(message.GetID()).BuildRouter(ModuleNameEdged, GroupResource, "test/"+model.ResourceTypeConfigmap, model.QueryOperation).FillBody("TestMessage")
	beehiveContext.SendResp(*msg)
	beehiveContext.Receive(ModuleNameEdgeHub)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("ProcessRemoteQueryDbFail", func(t *testing.T) {
		want := "TestMessage"
		if message.GetContent() != want {
			t.Errorf("Wrong message received : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//No error and connected true
	fakeDao := new([]dao.Meta)
	fakeDaoArray := make([]dao.Meta, 1)
	fakeDaoArray[0] = dao.Meta{Key: "Test", Value: MessageTest}
	fakeDao = &fakeDaoArray
	querySetterMock.EXPECT().All(gomock.Any()).SetArg(0, *fakeDao).Return(int64(1), nil).Times(1)
	querySetterMock.EXPECT().Filter(gomock.Any(), gomock.Any()).Return(querySetterMock).Times(1)
	ormerMock.EXPECT().QueryTable(gomock.Any()).Return(querySetterMock).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeRule, model.QueryOperation)
	meta.processQuery(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("LocalDatabaseNoErrorAndMetaFound", func(t *testing.T) {
		want := make([]string, 1)
		want[0] = MessageTest
		bytesWant, _ := json.Marshal(want)
		bytesGot, _ := json.Marshal(message.GetContent())
		if string(bytesGot) != string(bytesWant) {
			t.Errorf("Wrong message receive : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//ResId Nil database error
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, model.ResourceTypePodStatus, OperationNodeConnection).FillBody(connect.CloudDisconnected)

	querySetterMock.EXPECT().All(gomock.Any()).Return(int64(1), errFailedDBOperation).Times(1)
	querySetterMock.EXPECT().Filter(gomock.Any(), gomock.Any()).Return(querySetterMock).Times(1)
	ormerMock.EXPECT().QueryTable(gomock.Any()).Return(querySetterMock).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeRule, model.QueryOperation)
	meta.processQuery(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("ResIDNilDatabaseError", func(t *testing.T) {
		want := "failed to query meta in DB: " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong message receive : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//ResID not nil database error
	querySetterMock.EXPECT().All(gomock.Any()).Return(int64(1), errFailedDBOperation).Times(1)
	querySetterMock.EXPECT().Filter(gomock.Any(), gomock.Any()).Return(querySetterMock).Times(1)
	ormerMock.EXPECT().QueryTable(gomock.Any()).Return(querySetterMock).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeRule+"/test", model.QueryOperation)
	//beehiveContext.Send(MetaManagerModuleName, *msg)
	meta.processQuery(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdgeHub)
	t.Run("ResIDNotNilDatabaseError", func(t *testing.T) {
		want := "failed to query meta in DB: " + FailedDBOperation
		if message.GetContent() != want {
			t.Errorf("Wrong message receive : Wanted %v and Got %v", want, message.GetContent())
		}
	})

	//ResID not nil Success Case
	querySetterMock.EXPECT().All(gomock.Any()).SetArg(0, *fakeDao).Return(int64(1), nil).Times(1)
	querySetterMock.EXPECT().Filter(gomock.Any(), gomock.Any()).Return(querySetterMock).Times(1)
	ormerMock.EXPECT().QueryTable(gomock.Any()).Return(querySetterMock).Times(1)
	msg = model.NewMessage("").BuildRouter(ModuleNameEdgeHub, GroupResource, "test/"+model.ResourceTypeRule+"/test", model.QueryOperation)
	//beehiveContext.Send(MetaManagerModuleName, *msg)
	meta.processQuery(*msg)
	message, _ = beehiveContext.Receive(ModuleNameEdged)
	t.Run("DatabaseNoErrorAndMetaFound", func(t *testing.T) {
		want := make([]string, 1)
		want[0] = MessageTest
		bytesWant, _ := json.Marshal(want)
		bytesGot, _ := json.Marshal(message.GetContent())
		if string(bytesGot) != string(bytesWant) {
			t.Errorf("Wrong message receive : Wanted %v and Got %v", want, message.GetContent())
		}
	})
}
