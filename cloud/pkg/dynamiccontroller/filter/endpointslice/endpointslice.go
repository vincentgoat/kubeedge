package endpointslice

import (
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/kubeedge/kubeedge/cloud/pkg/dynamiccontroller/application"
	"github.com/kubeedge/kubeedge/cloud/pkg/dynamiccontroller/filter"
	"github.com/kubeedge/kubeedge/cloud/pkg/groupingcontroller/nodegroup"
)

// FilterImpl implement enpointslice filter
type FilterImpl struct {
	NodesInformer    *application.CommonResourceEventHandler
	ServicesInformer *application.CommonResourceEventHandler
}

const (
	resourceName = "EndpointSlice"
	filterName   = "EndpointSlice"
)

func newEndpointsliceFilter() *FilterImpl {
	return &FilterImpl{}
}

func Register() {
	filter.Register(newEndpointsliceFilter())
}

func (f *FilterImpl) Name() string {
	return filterName
}

func (f *FilterImpl) NeedFilter(content interface{}) bool {
	if objList, ok := content.(*unstructured.UnstructuredList); ok {
		if len(objList.Items) != 0 && objList.Items[0].GetObjectKind().GroupVersionKind().Kind == resourceName {
			return true
		}
		return false
	}
	if obj, ok := content.(*unstructured.Unstructured); ok {
		if obj.GetObjectKind().GroupVersionKind().Kind == resourceName {
			return true
		}
	}
	return false
}

func (f *FilterImpl) FilterResource(targetNode string, obj runtime.Object) {
	unstruct, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	var epSlice discovery.EndpointSlice
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstruct.UnstructuredContent(), &epSlice)
	if err != nil {
		klog.Errorf("convert unstructure content %v err: %v", unstruct.GetName(), err)
		return
	}
	var svcTopology string
	if svcName, ok := epSlice.Labels[discovery.LabelServiceName]; ok {
		if svcRaw, err := filter.GetDynamicResourceInformer(v1.SchemeGroupVersion.WithResource("services")).Lister().ByNamespace(epSlice.Namespace).Get(svcName); err != nil {
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
		klog.V(4).Info("skip filter for endpointSlice %v", unstruct.GetName())
		return
	}
	var epsTmp []discovery.Endpoint
	for _, ep := range epSlice.Endpoints {
		if filter.IsBelongToSameGroup(targetNode, *ep.NodeName) {
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
}

