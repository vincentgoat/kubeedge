package application

import (
	"strings"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/klog/v2"

	"github.com/kubeedge/beehive/pkg/core/model"
	"github.com/kubeedge/kubeedge/cloud/pkg/common/messagelayer"
	commoninformers "github.com/kubeedge/kubeedge/cloud/pkg/common/informers"
	"github.com/kubeedge/kubeedge/cloud/pkg/common/modules"
	"github.com/kubeedge/kubeedge/cloud/pkg/edgecontroller/constants"
	"github.com/kubeedge/kubeedge/cloud/pkg/groupingcontroller/nodegroup"
	v2 "github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao/v2"
	"github.com/kubeedge/kubeedge/pkg/metaserver/util"
)

type SelectorListener struct {
	id       string
	nodeName string
	gvr      schema.GroupVersionResource
	// e.g. labels and fields(metadata.namespace metadata.name spec.nodename)
	selector LabelFieldSelector
}

func NewSelectorListener(nodeName string, gvr schema.GroupVersionResource, selector LabelFieldSelector) *SelectorListener {
	return &SelectorListener{id: uuid.New().String(), nodeName: nodeName, gvr: gvr, selector: selector}
}

func (l *SelectorListener) sendAllObjects(rets []runtime.Object, handler *CommonResourceEventHandler) {
	for _, ret := range rets {
		event := watch.Event{
			Type:   watch.Added,
			Object: ret,
		}
		l.sendObj(event, handler.messageLayer)
	}
}

func isBelongToSameGroup(targetNodeName string, epNodeName string) bool {
	if strings.Compare(targetNodeName, epNodeName) == 0 {
		return true
	}
	targetNode, err := getDynamicResourceInformer(v1.SchemeGroupVersion.WithResource("nodes")).Lister().Get(targetNodeName)
	if err != nil {
		klog.Errorf("node informer get node %s error: %v", targetNodeName, err)
		return false
	}
	targetAccessor, err := meta.Accessor(targetNode)
	if err != nil {
		klog.Error(err)
		return false
	}

	epNode, err := getDynamicResourceInformer(v1.SchemeGroupVersion.WithResource("nodes")).Lister().Get(epNodeName)
	if err != nil {
		klog.Errorf("node informer get endpoint slice belonging node %s error: %v", epNodeName, err)
		return false
	}
	epNodeAccessor, err := meta.Accessor(epNode)
	if err != nil {
		klog.Error(err)
		return false
	}

	klog.Errorf("====node label %v ep node %v", targetAccessor.GetLabels(), epNodeAccessor.GetLabels())

	return targetAccessor.GetLabels()[nodegroup.LabelBelongingTo] == epNodeAccessor.GetLabels()[nodegroup.LabelBelongingTo]
}

func getDynamicResourceInformer(gvr schema.GroupVersionResource) informers.GenericInformer {
	return commoninformers.GetInformersManager().GetDynamicSharedInformerFactory().ForResource(gvr)
}

func filterEndpointSlice(obj runtime.Object, targetNode string) {
	if obj.GetObjectKind().GroupVersionKind().Kind != "EndpointSlice" {
		klog.Errorf("====not endpoint slice %v, unknown type: %T, ignore", obj.GetObjectKind().GroupVersionKind(), obj)
		return
	}
	unstruct, isUnstr := obj.(*unstructured.Unstructured)
	if !isUnstr {
		klog.Errorf("====not Unstructured %v, unknown type: %T, ignore", obj.GetObjectKind().GroupVersionKind(), obj)
		return
	}
	var epSlice discovery.EndpointSlice
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstruct.UnstructuredContent(), &epSlice)
	if err != nil {
		klog.Errorf("convert unstructure content err: %v", err)
		return
	}

	var svcTopology string
	if svcName, ok := epSlice.Labels[discovery.LabelServiceName]; ok {
		if svcRaw, err := getDynamicResourceInformer(v1.SchemeGroupVersion.WithResource("services")).Lister().ByNamespace(epSlice.Namespace).Get(svcName); err != nil {
			klog.Errorf("filter endpoint slice for svc %s error: %v", svcName, err)
			return
		} else {
			svcObj, err := meta.Accessor(svcRaw)
			if err != nil {
				klog.Errorf("get service %v accessor error: %v", svcName, err)
				return
			}
			svcTopology = svcObj.GetAnnotations()[nodegroup.ServiceTopologyAnnotation]
		}
	}
	if svcTopology != nodegroup.ServiceTopologyRangeNodegroup {
		klog.Errorf("====not svc group %v", svcTopology)
		return
	}
	klog.Errorf("====target node %v old endpoint slice %v", targetNode, epSlice.Endpoints)
	var epsTmp []discovery.Endpoint
	for _, ep := range epSlice.Endpoints {
		if isBelongToSameGroup(targetNode, *ep.NodeName) {
			epsTmp = append(epsTmp, ep)
		}
	}
	epSlice.Endpoints = epsTmp
	unstrRaw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&epSlice)
	if err != nil {
		klog.Errorf("endpointslice %v convert to unstructure error: %v", epSlice.Name, err)
		return
	}
	unstruct.SetUnstructuredContent(unstrRaw)
	klog.Errorf("====target node %v new endpoint slice %v", targetNode, epSlice.Endpoints)

	// ========
	test, ok := obj.(*unstructured.Unstructured)
	if !ok {
		klog.Errorf("====not Unstructured %v, unknown type: %T, ignore", obj.GetObjectKind().GroupVersionKind(), obj)
		return
	}
	klog.Errorf("====result: %v", test.UnstructuredContent())
	return
}

func (l *SelectorListener) sendObj(event watch.Event, messageLayer messagelayer.MessageLayer) {
	accessor, err := meta.Accessor(event.Object)
	if err != nil {
		klog.Error(err)
		return
	}
	klog.V(4).Infof("[dynamiccontroller/selectorListener] listener(%v) is sending obj %v", *l, accessor.GetName())
	// do not send obj if obj does not match listener's selector
	if !l.selector.MatchObj(event.Object) {
		return
	}

	filterEvent := *(event.DeepCopy())
	filterEndpointSlice(filterEvent.Object, l.nodeName)

	namespace := accessor.GetNamespace()
	if namespace == "" {
		namespace = v2.NullNamespace
	}
	kind := util.UnsafeResourceToKind(l.gvr.Resource)
	resourceType := strings.ToLower(kind)
	resource, err := messagelayer.BuildResource(l.nodeName, namespace, resourceType, accessor.GetName())
	if err != nil {
		klog.Warningf("built message resource failed with error: %s", err)
		return
	}

	var operation string
	switch filterEvent.Type {
	case watch.Added:
		operation = model.InsertOperation
	case watch.Modified:
		operation = model.UpdateOperation
	case watch.Deleted:
		operation = model.DeleteOperation
	default:
		klog.Warningf("event type: %s unsupported", filterEvent.Type)
		return
	}

	msg := model.NewMessage("").
		SetResourceVersion(accessor.GetResourceVersion()).
		BuildRouter(modules.DynamicControllerModuleName, constants.GroupResource, resource, operation).
		FillBody(filterEvent.Object)

	if err := messageLayer.Send(*msg); err != nil {
		klog.Warningf("send message failed with error: %s, operation: %s, resource: %s", err, msg.GetOperation(), msg.GetResource())
	} else {
		klog.V(4).Infof("send message successfully, operation: %s, resource: %s", msg.GetOperation(), msg.GetResource())
	}
}
