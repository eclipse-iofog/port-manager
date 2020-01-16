package manager

import (
	"context"
	"fmt"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"
)

const (
	controllerServiceName = "controller"
	controllerPort        = 51121
	proxyImage            = "quay.io/skupper/icproxy"
)

type Manager struct {
	watchNamespace string
	config         *rest.Config
	iofogUserEmail string
	iofogUserPass  string
	msvcPorts      map[string]map[int]bool
	k8sClient      k8sclient.Client
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

func (mgr *Manager) Run() (err error) {
	// Instantiate Kubernetes client
	mgr.k8sClient, err = k8sclient.New(mgr.config, k8sclient.Options{})
	if err != nil {
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
		time.Sleep(time.Second * 4)
		// Check ports
		msvcs, err := ioClient.GetAllMicroservices()
		if err != nil {
			continue
		}

		// Create/update resources based on microservice port state
		for _, msvc := range msvcs.Microservices {
			_, exists := mgr.msvcPorts[msvc.UUID]
			if exists {
				// Microservice already stored in cache
				if err := mgr.handleCachedMicroservice(msvc); err != nil {
					return err
				}
			} else {
				// Microservice not stored in cache
				if err := mgr.updateProxy(msvc); err != nil {
					return err
				}
			}
		}
		// Delete resources for erased microservices
		for cachedMsvc := range mgr.msvcPorts {
			found := false
			for _, msvc := range msvcs.Microservices {
				if msvc.UUID == cachedMsvc {
					found = true
				}
			}
			// Cached microservice not found in backend
			if !found {
				//TODO: Delete resources
				// Remove microservice from cache
				delete(mgr.msvcPorts, cachedMsvc)
			}
		}

	}
}

func (mgr *Manager) handleCachedMicroservice(msvc ioclient.MicroserviceInfo) error {
	cachedPorts := mgr.msvcPorts[msvc.UUID]
	// We must find the ports that have been created or removed
	// Find any newly added ports
	for _, msvcPort := range msvc.Ports {
		if _, exists := cachedPorts[msvcPort.External]; !exists {
			// Make updates with K8s API Server
			return mgr.updateProxy(msvc)
		}
	}
	// Find any removed ports
	for cachedPort := range cachedPorts {
		found := false
		for _, msvcPort := range msvc.Ports {
			if msvcPort.External == cachedPort {
				found = true
			}
		}
		// Did not find cached port in backend, delete cached port
		if !found {
			// Make updates with K8s API Server
			return mgr.updateProxy(msvc)
		}
	}

	return nil
}

func (mgr *Manager) updateProxy(msvc ioclient.MicroserviceInfo) error {
	// Key to check resources don't already exist
	proxyKey := k8sclient.ObjectKey{
		Name:      getProxyName(msvc.Name),
		Namespace: mgr.watchNamespace,
	}

	// Deployment
	dep := newProxyDeployment(mgr.watchNamespace, msvc.Name, createProxyConfig(&msvc), proxyImage, 1)
	if err := mgr.createOrUpdate(proxyKey, dep); err != nil {
		return err
	}

	// Service
	svc := newProxyService(mgr.watchNamespace, msvc.Name, msvc.Ports)
	if err := mgr.createOrUpdate(proxyKey, svc); err != nil {
		return err
	}

	// Update cache with new ports
	mgr.msvcPorts[msvc.UUID] = make(map[int]bool)
	for _, msvcPort := range msvc.Ports {
		mgr.msvcPorts[msvc.UUID][msvcPort.External] = true
	}

	return nil
}

func (mgr *Manager) createOrUpdate(objKey k8sclient.ObjectKey, obj runtime.Object) error {
	found := obj.DeepCopyObject()
	if err := mgr.k8sClient.Get(context.TODO(), objKey, found); err == nil {
		// Service found, update ports
		if err := mgr.k8sClient.Update(context.TODO(), obj); err != nil {
			return err
		}
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Service not found, create one
		if err := mgr.k8sClient.Create(context.TODO(), obj); err != nil {
			return err
		}
	}
	return nil
}
