package manager

import (
	"fmt"
	"time"

	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"
)

const (
	controllerServiceName = "controller"
	controllerPort        = 51121
)

type Manager struct {
	watchNamespace string
	config         *rest.Config
	iofogUserEmail string
	iofogUserPass  string
	msvcPorts      map[string]map[int]bool
}

func New(watchNamespace, iofogUserEmail, iofogUserPass string, config *rest.Config) *Manager {
	return &Manager{
		watchNamespace: watchNamespace,
		config:         config,
		iofogUserEmail: iofogUserEmail,
		iofogUserPass:  iofogUserPass,
		msvcPorts:      make(map[string]map[int]bool),
	}
}

func (mgr *Manager) Run() error {
	// Instantiate Kubernetes client
	if _, err := k8sclient.New(mgr.config, k8sclient.Options{}); err != nil {
		return err
	}

	// Instantiate ioFog client
	controllerEndpoint := fmt.Sprintf("%s.%s:%d", controllerServiceName, mgr.watchNamespace, controllerPort)
	ioClient, err := ioclient.NewAndLogin(controllerEndpoint, mgr.iofogUserEmail, mgr.iofogUserPass)
	if err != nil {
		return err
	}

	// Watch Controller API
	for {
		time.Sleep(time.Second * 2)
		// Check ports
		msvcs, err := ioClient.GetAllMicroservices()
		if err != nil {
			continue
		}

		// Create / delete resources based on port state
		for _, msvc := range msvcs.Microservices {
			cachedPorts, exists := mgr.msvcPorts[msvc.UUID]
			if exists {
				// Microservice already stored in cache
				if err := mgr.handleCachedMicroservice(cachedPorts, msvc.Ports); err != nil {
					return err
				}
			} else {
				// Microservice not stored in cache
				if err := mgr.handleNewMicroservice(msvc.UUID, msvc.Ports); err != nil {
					return err
				}
			}
		}
	}
}

func (mgr *Manager) handleCachedMicroservice(cachedPorts map[int]bool, backendPorts []ioclient.MicroservicePortMapping) error {
	// We must find the ports that have been created or removed
	// Find any newly added ports
	for _, backendPort := range backendPorts {
		if _, exists := cachedPorts[backendPort.External]; !exists {
			// Add resources for new backend port
		}
	}
	// Find any removed ports
	for cachedPort := range cachedPorts {
		found := false
		for _, backendPort := range backendPorts {
			if backendPort.External == cachedPort {
				found = true
			}
		}
		// Did not find cached port in backend, delete cached port
		if !found {
			// Remove resources for deleted cached port
		}
	}
	return nil
}

func (mgr *Manager) handleNewMicroservice(uuid string, backendPorts []ioclient.MicroservicePortMapping) error {
	// Microservice is not in cache
	mgr.msvcPorts[uuid] = make(map[int]bool)
	// Add ports to cache
	for _, backendPort := range backendPorts {
		// Add resources for new ports
		// ...
		// Add port to cache
		mgr.msvcPorts[uuid][backendPort.External] = true
	}
	return nil
}
