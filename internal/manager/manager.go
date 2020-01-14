package manager

import (
	"fmt"

	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"
)

const (
	controllerServiceName = "controller"
	controllerPort        = 51121
)

type Manager struct {
	watchNamespace  string
	config          *rest.Config
	controllerToken string
}

func New(watchNamespace, controllerToken string, config *rest.Config) *Manager {
	return &Manager{
		watchNamespace:  watchNamespace,
		config:          config,
		controllerToken: controllerToken,
	}
}

func (mgr *Manager) Run() error {
	// Instantiate Kubernetes client
	if _, err := k8sclient.New(mgr.config, k8sclient.Options{}); err != nil {
		return err
	}

	// Instantiate ioFog client
	controllerEndpoint := fmt.Sprintf("%s.%s:%d", controllerServiceName, mgr.watchNamespace, controllerPort)
	_, err := ioclient.NewWithToken(controllerEndpoint, mgr.controllerToken)
	if err != nil {
		return err
	}

	// Watch Controller API
	for {

	}

	return nil
}
