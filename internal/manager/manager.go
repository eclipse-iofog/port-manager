package manager

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

func (mgr *Manager) handleNewMicroservice(msvc ioclient.MicroserviceInfo) error {
	// Microservice is not in cache
	mgr.msvcPorts[msvc.UUID] = make(map[int]bool)
	// Add ports to cache
	for _, msvcPort := range msvc.Ports {
		// Add resources for new ports
		// ...
		// Add port to cache
		mgr.msvcPorts[msvc.UUID][msvcPort.External] = true
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
	dep := &appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, dep); err == nil {
		// Deployment found
		// Read the existing deployment's config
		config, err := getProxyConfig(dep)
		if err != nil {
			return err
		}
		// Add any new ports to the config
		for _, msvcPort := range msvc.Ports {
			if !strings.Contains(config, strconv.Itoa(msvcPort.External)) {
				// Update config string
				config = fmt.Sprintf("%s,%s", config, createProxyConfig(msvc.Name, msvc.UUID, msvcPort.External))
			}
		}
		// Remove any old ports from the config
		configPorts, err := decodeConfig(config)
		if err != nil {
			return err
		}
		for _, configPort := range configPorts {
			found := false
			for _, msvcPort := range msvc.Ports {
				if configPort == msvcPort.External {
					found = true
					break
				}
			}
			if !found {
				// Remove port from config
				rmvSubstr := createProxyConfig(msvc.Name, msvc.UUID, configPort)
				config = strings.Replace(config, ","+rmvSubstr, "", 1)
				config = strings.Replace(config, rmvSubstr, "", 1)
			}
		}
		// Update the deployment
		if err := updateProxyConfig(dep, config); err != nil {
			return err
		}
		if err := mgr.k8sClient.Update(context.TODO(), dep); err != nil {
			return err
		}
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Deployment not found, create one
		// Generate config
		config := ""
		for idx, msvcPort := range msvc.Ports {
			separator := ""
			if idx != 0 {
				separator = ","
			}
			config = fmt.Sprintf("%s%s%s", config, separator, createProxyConfig(msvc.Name, msvc.UUID, msvcPort.External))
		}
		dep = newProxyDeployment(mgr.watchNamespace, msvc.Name, config, proxyImage, 1)
		if err := mgr.k8sClient.Create(context.TODO(), dep); err != nil {
			return err
		}
	}

	// Service
	svc := &corev1.Service{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, dep); err == nil {
		// Service found, update ports
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Service not found, create one
		svc = newProxyService(mgr.watchNamespace, msvc.Name, msvcPort)
		if err := mgr.k8sClient.Create(context.TODO(), svc); err != nil {
			return err
		}
	}

	// Update cache with new ports
	mgr.msvcPorts[msvc.UUID] = make(map[int]bool)
	for _, msvcPort := range msvc.Ports {
		mgr.msvcPorts[msvc.UUID][msvcPort.External] = true
	}

	return nil
}
