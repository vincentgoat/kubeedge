package policycontroller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/kubeedge/beehive/pkg/core"
	beehiveContext "github.com/kubeedge/beehive/pkg/core/context"
	"github.com/kubeedge/kubeedge/cloud/pkg/common/client"
	"github.com/kubeedge/kubeedge/cloud/pkg/common/messagelayer"
	"github.com/kubeedge/kubeedge/cloud/pkg/common/modules"
	"github.com/kubeedge/kubeedge/cloud/pkg/policycontroller/controller"
	policyv1alpha1 "github.com/kubeedge/kubeedge/pkg/apis/policy/v1alpha1"
)

// policycontroller use beehive context message layer
type policycontroller struct {
	manager manager.Manager
	ctx     context.Context
}

var _ core.Module = (*policycontroller)(nil)

var accessScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(scheme.AddToScheme(accessScheme))
	utilruntime.Must(policyv1alpha1.AddToScheme(accessScheme))
}

func NewAccessRoleControllerManager(ctx context.Context, kubeCfg *rest.Config) (manager.Manager, error) {
	controllerManager, err := controllerruntime.NewManager(kubeCfg, controllerruntime.Options{
		Scheme: accessScheme,
		// TODO: leader election
		// TODO: /healthz
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller manager, %v", err)
	}

	if err := setupControllers(ctx, controllerManager); err != nil {
		return nil, err
	}
	return controllerManager, nil
}

func setupControllers(ctx context.Context, mgr manager.Manager) error {
	// This returned cli will directly acquire the unstructured objects from API Server which
	// have not be registered in the accessScheme.
	cli := mgr.GetClient()
	pc := &controller.Controller{
		Client:       cli,
		MessageLayer: messagelayer.PolicyControllerMessageLayer(),
	}

	klog.Info("setup policy controller")
	if err := pc.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to setup nodegroup controller, %v", err)
	}
	return nil
}

func Register() {
	var pc = &policycontroller{}
	pc.ctx = beehiveContext.GetContext()
	mgr, err := NewAccessRoleControllerManager(pc.ctx, client.KubeConfig)
	if err != nil {
		klog.Fatalf("failed to create controller manager, %v", err)
	}
	pc.manager = mgr
	core.Register(pc)
}

// Name of controller
func (pc *policycontroller) Name() string {
	return modules.PolicyControllerModuleName
}

// Group of controller
func (pc *policycontroller) Group() string {
	return modules.PolicyControllerGroupName
}

// Enable indicates whether enable this module
func (pc *policycontroller) Enable() bool {
	return true
}

// Start controller
func (pc *policycontroller) Start() {
	// mgr.Start will block until the manager has stopped
	if err := pc.manager.Start(pc.ctx); err != nil {
		klog.Fatalf("failed to start controller manager, %v", err)
	}
}
