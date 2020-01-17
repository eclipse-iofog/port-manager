package manager

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"

	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const (
	controllerServiceName = "controller"
	controllerPort        = 51121
	proxyImage            = "quay.io/skupper/icproxy"
	managerName           = "port-manager"
	pollInterval          = time.Second * 4
)

type Manager struct {
	namespace      string
	config         *rest.Config
	iofogUserEmail string
	iofogUserPass  string
	msvcCache      map[string]*ioclient.MicroserviceInfo
	k8sClient      k8sclient.Client
	log            logr.Logger
	owner          metav1.OwnerReference
}

func New(namespace, iofogUserEmail, iofogUserPass string, config *rest.Config) *Manager {
	logf.SetLogger(logf.ZapLogger(false))
	return &Manager{
		namespace:      namespace,
		config:         config,
		iofogUserEmail: iofogUserEmail,
		iofogUserPass:  iofogUserPass,
		msvcCache:      make(map[string]*ioclient.MicroserviceInfo),
		log:            logf.Log.WithName(managerName),
	}
}

// Query the K8s API Server for details of this pod's deployment
// Store details for later use when assigning owners to other K8s resources we make
// Owner reference is required for automatic cleanup of K8s resources made by this runtime
func (mgr *Manager) getOwnerReference() error {
	objKey := k8sclient.ObjectKey{
		Name:      managerName,
		Namespace: mgr.namespace,
	}
	dep := appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), objKey, &dep); err != nil {
		return err
	}
	mgr.owner = metav1.OwnerReference{
		APIVersion: "extensions/v1beta1",
		Kind:       "Deployment",
		Name:       dep.Name,
		UID:        dep.UID,
	}
	return nil
}

// Main loop of manager
// Query ioFog Controller REST API and compare against cache
// Make updates to K8s resources as required
func (mgr *Manager) Run() (err error) {
	// Instantiate Kubernetes client
	mgr.k8sClient, err = k8sclient.New(mgr.config, k8sclient.Options{})
	if err != nil {
		return err
	}
	mgr.log.Info("Created Kubernetes client")

	// Get owner reference
	if err := mgr.getOwnerReference(); err != nil {
		return err
	}
	mgr.log.Info("Got owner reference from Kubernetes API Server")

	// Instantiate ioFog client
	controllerEndpoint := fmt.Sprintf("%s.%s:%d", controllerServiceName, mgr.namespace, controllerPort)
	ioClient, err := ioclient.NewAndLogin(controllerEndpoint, mgr.iofogUserEmail, mgr.iofogUserPass)
	if err != nil {
		return err
	}
	mgr.log.Info("Logged into Controller API")

	// Watch Controller API
	for {
		time.Sleep(pollInterval)

		mgr.log.Info("Polling Controller API")
		// Check ports
		msvcs, err := ioClient.GetAllMicroservices()
		if err != nil {
			return err
		}
		mgr.log.Info(fmt.Sprintf("Found %d Microservices", len(msvcs.Microservices)))

		// Create/update resources based on microservice port state
		for _, msvc := range msvcs.Microservices {
			_, exists := mgr.msvcCache[msvc.UUID]
			if exists {
				// Microservice already stored in cache
				if err := mgr.handleCachedMicroservice(msvc); err != nil {
					return err
				}
			} else {
				// Microservice not stored in cache
				if hasPublicPorts(msvc) {
					if err := mgr.updateProxy(msvc); err != nil {
						return err
					}
				}
			}
		}

		// Delete resources for erased microservices
		// Build map to avoid O(N^2) algorithm where N is msvc count
		backendMsvcs := make(map[string]*ioclient.MicroserviceInfo)
		for _, msvc := range msvcs.Microservices {
			backendMsvcs[msvc.UUID] = &msvc
		}
		// Compare cache to backend
		for _, cachedMsvc := range mgr.msvcCache {
			// If match, continue
			if _, exists := backendMsvcs[cachedMsvc.UUID]; exists {
				continue
			}
			// Cached microservice not found in backend
			// Delete resources from K8s API Server
			if err := mgr.deleteProxy(cachedMsvc.Name); err != nil {
				return err
			}
			// Remove microservice from cache
			delete(mgr.msvcCache, cachedMsvc.UUID)
		}

	}
}

// Update K8s resources for a Microservice found in this runtime's cache
func (mgr *Manager) handleCachedMicroservice(msvc ioclient.MicroserviceInfo) error {
	// Find any newly added ports
	// Build map to avoid O(N^2) algorithm where N is msvc port count
	cachedPorts := buildPortMap(mgr.msvcCache[msvc.UUID].Ports)
	for _, msvcPort := range msvc.Ports {
		if _, exists := cachedPorts[msvcPort.External]; !exists {
			// Make updates with K8s API Server
			return mgr.updateProxy(msvc)
		}
	}
	// Find any removed ports
	// Build map to avoid O(N^2) algorithm where N is msvc port count
	backendPorts := buildPortMap(msvc.Ports)
	for cachedPort := range cachedPorts {
		if _, exists := backendPorts[cachedPort]; !exists {
			// Did not find cached port in backend, delete cached port
			// Make updates with K8s API Server
			return mgr.updateProxy(msvc)
		}
	}

	return nil
}

// Delete all K8s resources for an HTTP Proxy created for a Microservice
func (mgr *Manager) deleteProxy(msvcName string) error {
	mgr.log.Info("Deleting Proxy resources for Microservice", "Microservice", msvcName)

	proxyKey := k8sclient.ObjectKey{
		Name:      getProxyName(msvcName),
		Namespace: mgr.namespace,
	}
	meta := metav1.ObjectMeta{
		Name:      getProxyName(msvcName),
		Namespace: mgr.namespace,
	}
	dep := &appsv1.Deployment{ObjectMeta: meta}
	if err := mgr.delete(proxyKey, dep); err != nil {
		return err
	}
	svc := &corev1.Service{ObjectMeta: meta}
	if err := mgr.delete(proxyKey, svc); err != nil {
		return err
	}
	return nil
}

// Create or update an HTTP Proxy instance for a Microservice
func (mgr *Manager) updateProxy(msvc ioclient.MicroserviceInfo) error {
	mgr.log.Info("Updating Proxy resources for Microservice", "Microservice", msvc.Name)

	// Key to check resources don't already exist
	proxyKey := k8sclient.ObjectKey{
		Name:      getProxyName(msvc.Name),
		Namespace: mgr.namespace,
	}

	// Deployment
	dep := newProxyDeployment(mgr.namespace, msvc.Name, createProxyConfig(&msvc), proxyImage, 1)
	mgr.setOwnerReference(dep)
	if err := mgr.createOrUpdate(proxyKey, dep); err != nil {
		return err
	}

	// Service
	foundSvc := corev1.Service{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundSvc); err == nil {
		// Existing service found, update it without touching immutable values
		foundSvc.Spec.Ports = getServicePorts(msvc.Ports)
		if err := mgr.k8sClient.Update(context.TODO(), &foundSvc); err != nil {
			return err
		}
	} else {
		// Create new service
		svc := newProxyService(mgr.namespace, msvc.Name, msvc.Ports)
		mgr.setOwnerReference(svc)
		if err := mgr.k8sClient.Create(context.TODO(), svc); err != nil {
			return err
		}
	}

	// Update cache
	mgr.msvcCache[msvc.UUID] = &msvc

	return nil
}

func (mgr *Manager) delete(objKey k8sclient.ObjectKey, obj runtime.Object) error {
	if err := mgr.k8sClient.Delete(context.Background(), obj); err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		return err
	}
	return nil
}

func (mgr *Manager) createOrUpdate(objKey k8sclient.ObjectKey, obj runtime.Object) error {
	found := obj.DeepCopyObject()
	if err := mgr.k8sClient.Get(context.TODO(), objKey, found); err == nil {
		// Resource found, update ports
		if err := mgr.k8sClient.Update(context.TODO(), obj); err != nil {
			return err
		}
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Resource not found, create one
		if err := mgr.k8sClient.Create(context.TODO(), obj); err != nil {
			return err
		}
	}
	return nil
}

func (mgr *Manager) setOwnerReference(obj metav1.Object) {
	obj.SetOwnerReferences([]metav1.OwnerReference{mgr.owner})
}

func hasPublicPorts(msvc ioclient.MicroserviceInfo) bool {
	for _, msvcPort := range msvc.Ports {
		if msvcPort.External != 0 {
			return true
		}
	}
	return false
}

func buildPortMap(ports []ioclient.MicroservicePortMapping) map[int]bool {
	portMap := make(map[int]bool)
	for _, port := range ports {
		if port.External != 0 {
			portMap[port.External] = true
		}
	}
	return portMap
}
