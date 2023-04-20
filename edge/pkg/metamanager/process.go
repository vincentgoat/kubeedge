package metamanager

import (
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	beehiveContext "github.com/kubeedge/beehive/pkg/core/context"
	"github.com/kubeedge/beehive/pkg/core/model"
	cloudmodules "github.com/kubeedge/kubeedge/cloud/pkg/common/modules"
	"github.com/kubeedge/kubeedge/common/constants"
	connect "github.com/kubeedge/kubeedge/edge/pkg/common/cloudconnection"
	"github.com/kubeedge/kubeedge/edge/pkg/common/modules"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/client"
	metaManagerConfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver/kubernetes/storage/sqlite/imitator"
	policyv1alpha1 "github.com/kubeedge/kubeedge/pkg/apis/policy/v1alpha1"
)

// Constants to check metamanager processes
const (
	OK = "OK"

	GroupResource = "resource"

	CloudControllerModel = "edgecontroller"
)

func feedbackError(err error, request model.Message) {
	errResponse := model.NewErrorMessage(&request, err.Error()).SetRoute(modules.MetaManagerModuleName, request.GetGroup())
	if request.GetSource() == modules.EdgedModuleName {
		sendToEdged(errResponse, request.IsSync())
	} else {
		sendToCloud(errResponse)
	}
}

func feedbackResponse(message *model.Message, parentID string, resp *model.Message) {
	resp.BuildHeader(resp.GetID(), parentID, resp.GetTimestamp())
	sendToEdged(resp, message.IsSync())
	respToCloud := message.NewRespByMessage(resp, OK)
	sendToCloud(respToCloud)
}

func sendToEdged(message *model.Message, sync bool) {
	if sync {
		beehiveContext.SendResp(*message)
	} else {
		beehiveContext.Send(modules.EdgedModuleName, *message)
	}
}

func sendToCloud(message *model.Message) {
	beehiveContext.SendToGroup(string(metaManagerConfig.Config.ContextSendGroup), *message)
}

// Resource format: <namespace>/<restype>[/resid]
// return <reskey, restype, resid>
func parseResource(resource string) (string, string, string) {
	tokens := strings.Split(resource, constants.ResourceSep)
	resType := ""
	resID := ""
	switch len(tokens) {
	case 2:
		resType = tokens[len(tokens)-1]
	case 3:
		resType = tokens[len(tokens)-2]
		resID = tokens[len(tokens)-1]
	default:
	}
	return resource, resType, resID
}

// is resource type require remote query
func requireRemoteQuery(resType string) bool {
	return resType == model.ResourceTypeConfigmap ||
		resType == model.ResourceTypeSecret ||
		resType == constants.ResourceTypePersistentVolume ||
		resType == constants.ResourceTypePersistentVolumeClaim ||
		resType == constants.ResourceTypeVolumeAttachment ||
		resType == model.ResourceTypeNode ||
		resType == model.ResourceTypeServiceAccountToken ||
		resType == model.ResourceTypeLease
}

func msgDebugInfo(message *model.Message) string {
	return fmt.Sprintf("msgID[%s] resource[%s]", message.GetID(), message.GetResource())
}

func (m *metaManager) handleMixerResource(message *model.Message) error {
	am := &policyv1alpha1.AccessMixer{}
	if err := json.Unmarshal(message.GetContent().([]byte), am); err != nil {
		klog.Errorf("unmarshal message failed: %v", err)
		return fmt.Errorf("unmarshal message failed: %v", err)
	}
	if err := m.cachePolicyResource(am, message.GetOperation()); err != nil {
		klog.Errorf("parse policy resource failed: %v", err)
		return fmt.Errorf("parse policy resource failed: %v", err)
	}
	return nil
}

func (m *metaManager) handleMessage(message *model.Message) error {
	obj := message.GetContent()
	var runtimeObj runtime.Object
	var ok bool
	if runtimeObj, ok = obj.(runtime.Object); !ok {
		return fmt.Errorf("message content is not runtime object")
	}
	indexer := m.getIndexer(runtimeObj)

	_, ok = obj.(*policyv1alpha1.AccessMixer)
	if ok {
		if err := m.handleMixerResource(message); err != nil {
			klog.Errorf("handle mixer resource failed: %v", err)
			return fmt.Errorf("handle mixer resource failed: %v", err)
		}
	}
	resKey, resType, _ := parseResource(message.GetResource())
	switch message.GetOperation() {
	case model.InsertOperation, model.UpdateOperation, model.PatchOperation, model.ResponseOperation:
		if _, exist, err := indexer.Get(obj); err == nil && exist {
			if err := indexer.Update(obj); err != nil {
				klog.Errorf("update object failed: %v", err)
				return fmt.Errorf("update object failed: %v", err)
			}
		} else {
			if err := indexer.Add(obj); err != nil {
				klog.Errorf("add object failed: %v", err)
				return fmt.Errorf("add object failed: %v", err)
			}
		}
		content, err := message.GetContentData()
		if err != nil {
			klog.Errorf("handle [%s] message content data failed, %s", message.GetOperation(), msgDebugInfo(message))
			return fmt.Errorf("failed to handle [%s] message content data: %s", message.GetOperation(), err)
		}
		resKey, err = getSpecialResourceKey(resType, resKey, *message)
		if err != nil {
			klog.Errorf("get remote query response content data failed, %s", msgDebugInfo(message))
			return fmt.Errorf("failed to get remote query response message content data: %s", err)
		}
		meta := &dao.Meta{
			Key:   resKey,
			Type:  resType,
			Value: string(content)}
		err = dao.InsertOrUpdate(meta)
		if err != nil {
			klog.Errorf("insert or update meta failed, %s: %v", msgDebugInfo(message), err)
			return fmt.Errorf("failed to insert or update meta to DB: %s", err)
		}

	case model.DeleteOperation:
		var err error
		if err = indexer.Delete(message.GetContent()); err != nil {
			klog.Warningf("delete object failed: %v", err)
		}
		if resType == model.ResourceTypePod {
			err = processDeletePodDB(*message)
			if err != nil {
				klog.Errorf("delete pod meta failed, message %s, err: %v", msgDebugInfo(message), err)
				return fmt.Errorf("failed to delete pod meta to DB: %s", err)
			}
		} else {
			err = dao.DeleteMetaByKey(message.GetResource())
			if err != nil {
				klog.Errorf("delete meta failed, %s", msgDebugInfo(message))
				return fmt.Errorf("failed to delete meta to DB: %s", err)
			}
		}
	}
	return nil
}

func (m *metaManager) processInsert(message model.Message) {
	imitator.DefaultV2Client.Inject(message)
	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	_, resType, _ := parseResource(message.GetResource())
	if (resType == model.ResourceTypeNode || resType == model.ResourceTypeLease) && message.GetSource() == modules.EdgedModuleName {
		sendToCloud(&message)
		return
	}

	msgSource := message.GetSource()
	if msgSource == cloudmodules.DeviceControllerModuleName {
		message.SetRoute(modules.MetaGroup, modules.DeviceTwinModuleName)
		beehiveContext.Send(modules.DeviceTwinModuleName, message)
	} else {
		// Notify edged
		sendToEdged(&message, false)
	}

	resp := message.NewRespByMessage(&message, OK)
	sendToCloud(resp)
}

func (m *metaManager) processUpdate(message model.Message) {
	imitator.DefaultV2Client.Inject(message)

	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	msgSource := message.GetSource()
	switch msgSource {
	case modules.EdgedModuleName:
		sendToCloud(&message)
		// For pod status update message, we need to wait for the response message
		// to ensure that the pod status is correctly reported to the kube-apiserver
		_, resType, _ := parseResource(message.GetResource())
		if resType != model.ResourceTypePodStatus && resType != model.ResourceTypeLease {
			resp := message.NewRespByMessage(&message, OK)
			sendToEdged(resp, message.IsSync())
		}
	case cloudmodules.EdgeControllerModuleName, cloudmodules.DynamicControllerModuleName:
		sendToEdged(&message, message.IsSync())
		resp := message.NewRespByMessage(&message, OK)
		sendToCloud(resp)
	case cloudmodules.DeviceControllerModuleName:
		resp := message.NewRespByMessage(&message, OK)
		sendToCloud(resp)

		message.SetRoute(modules.MetaGroup, modules.DeviceTwinModuleName)
		beehiveContext.Send(modules.DeviceTwinModuleName, message)

	case cloudmodules.PolicyControllerGroupName:
		resp := message.NewRespByMessage(&message, OK)
		sendToCloud(resp)
	default:
		klog.Errorf("unsupport message source, %s", msgSource)
	}
}

func (m *metaManager) processPatch(message model.Message) {
	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}

	sendToCloud(&message)
}

func (m *metaManager) processResponse(message model.Message) {
	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	// Notify edged if the data is coming from cloud
	if message.GetSource() == CloudControllerModel {
		sendToEdged(&message, message.IsSync())
	} else {
		// Send to cloud if the update request is coming from edged
		sendToCloud(&message)
	}
}

func (m *metaManager) processDelete(message model.Message) {
	imitator.DefaultV2Client.Inject(message)
	_, resType, _ := parseResource(message.GetResource())
	if resType == model.ResourceTypePod && message.GetSource() == modules.EdgedModuleName {
		sendToCloud(&message)
		return
	}

	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	msgSource := message.GetSource()
	if msgSource == cloudmodules.DeviceControllerModuleName {
		message.SetRoute(modules.MetaGroup, modules.DeviceTwinModuleName)
		beehiveContext.Send(modules.DeviceTwinModuleName, message)
	}

	// Notify edged
	sendToEdged(&message, false)
	resp := message.NewRespByMessage(&message, OK)
	sendToCloud(resp)
}

func processDeletePodDB(message model.Message) error {
	podDBList, err := dao.QueryMeta("key", message.GetResource())
	if err != nil {
		return err
	}

	podList := *podDBList
	if len(podList) == 0 {
		klog.Infof("no pod with key %s key in DB", message.GetResource())
		return nil
	}

	var podDB corev1.Pod
	err = json.Unmarshal([]byte(podList[0]), &podDB)
	if err != nil {
		return err
	}

	var msgPod corev1.Pod
	msgContent, err := message.GetContentData()
	if err != nil {
		return err
	}

	err = json.Unmarshal(msgContent, &msgPod)
	if err != nil {
		return err
	}

	if podDB.UID != msgPod.UID {
		klog.Warning("pod UID is not equal to pod stored in DB, don't need to delete pod DB")
		return nil
	}

	err = dao.DeleteMetaByKey(message.GetResource())
	if err != nil {
		return err
	}

	podStatusKey := strings.Replace(message.GetResource(),
		constants.ResourceSep+model.ResourceTypePod+constants.ResourceSep,
		constants.ResourceSep+model.ResourceTypePodStatus+constants.ResourceSep, 1)
	err = dao.DeleteMetaByKey(podStatusKey)
	if err != nil {
		return err
	}

	podPatchKey := strings.Replace(message.GetResource(),
		constants.ResourceSep+model.ResourceTypePod+constants.ResourceSep,
		constants.ResourceSep+model.ResourceTypePodPatch+constants.ResourceSep, 1)
	err = dao.DeleteMetaByKey(podPatchKey)
	if err != nil {
		return err
	}

	return nil
}

// getSpecialResourceKey get service account db key
func getSpecialResourceKey(resType, resKey string, message model.Message) (string, error) {
	if resType != model.ResourceTypeServiceAccountToken {
		return resKey, nil
	}
	tokenReq, ok := message.GetContent().(*authenticationv1.TokenRequest)
	if !ok {
		return "", fmt.Errorf("failed to get resource %s name and namespace", resKey)
	}
	tokens := strings.Split(resKey, constants.ResourceSep)
	if len(tokens) != 3 {
		return "", fmt.Errorf("failed to get resource %s name and namespace", resKey)
	}
	return client.KeyFunc(tokens[2], tokens[0], tokenReq), nil
}

func (m *metaManager) processQuery(message model.Message) {
	resKey, resType, resID := parseResource(message.GetResource())
	var metas *[]string
	var err error
	if requireRemoteQuery(resType) && connect.IsConnected() {
		resKey, err = getSpecialResourceKey(resType, resKey, message)
		if err != nil {
			klog.Errorf("failed to get special resource %s key", resKey)
			return
		}
		metas, err = dao.QueryMeta("key", resKey)
		if err != nil || len(*metas) == 0 || resType == model.ResourceTypeNode || resType == constants.ResourceTypeVolumeAttachment || resType == model.ResourceTypeLease {
			m.processRemoteQuery(message)
		} else {
			resp := message.NewRespByMessage(&message, *metas)
			resp.SetRoute(modules.MetaManagerModuleName, resp.GetGroup())
			sendToEdged(resp, message.IsSync())
		}
		return
	}

	if resID == "" {
		// Get specific type resources
		metas, err = dao.QueryMeta("type", resType)
	} else {
		metas, err = dao.QueryMeta("key", resKey)
	}
	if err != nil {
		klog.Errorf("query meta failed, %s", msgDebugInfo(&message))
		feedbackError(fmt.Errorf("failed to query meta in DB: %s", err), message)
	} else {
		resp := message.NewRespByMessage(&message, *metas)
		resp.SetRoute(modules.MetaManagerModuleName, resp.GetGroup())
		sendToEdged(resp, message.IsSync())
	}
}

func (m *metaManager) processRemoteQuery(message model.Message) {
	go func() {
		// TODO: retry
		originalID := message.GetID()
		message.UpdateID()
		resp, err := beehiveContext.SendSync(
			string(metaManagerConfig.Config.ContextSendModule),
			message,
			time.Duration(metaManagerConfig.Config.RemoteQueryTimeout)*time.Second)
		if err != nil {
			klog.Errorf("remote query failed, req[%s], err: %v", msgDebugInfo(&message), err)
			feedbackError(fmt.Errorf("failed to query meta in DB: %s", err), message)
			return
		}
		errContent, ok := resp.GetContent().(error)
		if ok {
			klog.V(4).Infof("process remote query err: %v", errContent)
			feedbackResponse(&message, originalID, &resp)
			return
		}
		klog.V(4).Infof("process remote query: req[%s], resp[%s]", msgDebugInfo(&message), msgDebugInfo(&resp))
		if err := m.handleMessage(&resp); err != nil {
			feedbackError(fmt.Errorf("failed to query meta in DB: %s", err), message)
			return
		}

		feedbackResponse(&message, originalID, &resp)
	}()
}

func (m *metaManager) processVolume(message model.Message) {
	klog.Info("process volume started")
	back, err := beehiveContext.SendSync(modules.EdgedModuleName, message, constants.CSISyncMsgRespTimeout)
	klog.Infof("process volume get: req[%+v], back[%+v], err[%+v]", message, back, err)
	if err != nil {
		klog.Errorf("process volume send to edged failed: %v", err)
	}

	resp := message.NewRespByMessage(&message, back.GetContent())
	sendToCloud(resp)
	klog.Infof("process volume send to cloud resp[%+v]", resp)
}

func (m *metaManager) process(message model.Message) {
	operation := message.GetOperation()

	switch operation {
	case model.InsertOperation:
		m.processInsert(message)
	case model.UpdateOperation:
		m.processUpdate(message)
	case model.PatchOperation:
		m.processPatch(message)
	case model.DeleteOperation:
		m.processDelete(message)
	case model.QueryOperation:
		m.processQuery(message)
	case model.ResponseOperation:
		m.processResponse(message)
	case constants.CSIOperationTypeCreateVolume,
		constants.CSIOperationTypeDeleteVolume,
		constants.CSIOperationTypeControllerPublishVolume,
		constants.CSIOperationTypeControllerUnpublishVolume:
		m.processVolume(message)
	default:
		klog.Errorf("metamanager not supported operation: %v", operation)
	}
}

func (m *metaManager) runMetaManager() {
	go func() {
		for {
			select {
			case <-beehiveContext.Done():
				klog.Warning("MetaManager main loop stop")
				return
			default:
			}
			msg, err := beehiveContext.Receive(m.Name())
			if err != nil {
				klog.Errorf("get a message %+v: %v", msg, err)
				continue
			}
			klog.V(2).Infof("get a message %+v", msg)
			m.process(msg)
		}
	}()
}
