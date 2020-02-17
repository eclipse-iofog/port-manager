package manager

import (
	"context"
	"fmt"
	"strings"
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
	managerName           = "port-manager"
	pollInterval          = time.Second * 10
)

type portMap map[int]string // map[port]queue

type Manager struct {
	namespace      string
	config         *rest.Config
	iofogUserEmail string
	iofogUserPass  string
	cache          portMap
	k8sClient      k8sclient.Client
	log            logr.Logger
	owner          metav1.OwnerReference
	proxyImage     string
	routerAddress  string
}

func New(namespace, iofogUserEmail, iofogUserPass, proxyImage, routerAddress string, config *rest.Config) *Manager {
	logf.SetLogger(logf.ZapLogger(false))
	return &Manager{
		namespace:      namespace,
		config:         config,
		iofogUserEmail: iofogUserEmail,
		iofogUserPass:  iofogUserPass,
		cache:          make(portMap),
		log:            logf.Log.WithName(managerName),
		proxyImage:     proxyImage,
		routerAddress:  routerAddress,
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
	mgr.log.Info("Created Kubernetes clients")

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

	// Initialize cache based on K8s API
	if err := mgr.generateCache(ioClient); err != nil {
		return err
	}

	// Watch Controller API
	for {
		time.Sleep(pollInterval)
		if err := mgr.run(ioClient); err != nil {
			// Exit with error to reset the cache
			return err
		}
	}
}

func (mgr *Manager) generateCache(ioClient *ioclient.Client) error {
	mgr.log.Info("Generating cache based on Kubernetes API")

	// Get deployment
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.namespace,
	}
	foundDep := appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundDep); err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Deployment not found, no ports open, nothing to cache
		return nil
	}

	// Deployment exists, get the config
	config, err := getProxyConfig(&foundDep)
	if err != nil {
		return err
	}

	// Get microservices from config
	configItems := strings.Split(config, ",")
	for _, configItem := range configItems {
		// Get microservice and port details from item
		port, queue, err := decodeMicroservice(configItem)
		if err != nil {
			return err
		}
		// Update cache
		mgr.cache[port] = queue
	}

	fmt.Println(mgr.cache)
	return nil
}

func (mgr *Manager) run(ioClient *ioclient.Client) error {
	// Check ports
	backendPorts, err := ioClient.GetAllMicroservicePublicPorts()
	if err != nil {
		return err
	}

	// Update Proxy config if new ports are created or queues changed
	for _, backendPort := range backendPorts {
		port := backendPort.PublicPort.Port
		queue := backendPort.PublicPort.Queue
		existingQueue, exists := mgr.cache[port]
		// Microservice already stored in cache
		if exists {
			// Check for queue change
			if existingQueue != queue {
				// Update cache
				mgr.cache[port] = queue
				// Update K8s resources
				return mgr.updateProxy()
			}
			// Exists and queue is unchanged
			continue
		} else {
			// New port, update cache
			mgr.cache[port] = queue
			return mgr.updateProxy()
		}
	}

	// Update Proxy config if ports are deleted
	// Create map of backend ports
	backendPortMap := make(map[int]string)
	for _, backendPort := range backendPorts {
		backendPortMap[backendPort.PublicPort.Port] = backendPort.PublicPort.Queue
	}
	for port := range mgr.cache {
		// If match, continue
		if _, exists := backendPortMap[port]; exists {
			continue
		}
		// Cached microservice not found in backend
		// Remove microservice from cache
		delete(mgr.cache, port)
		// Delete resources from K8s API Server
		return mgr.updateProxy()
	}

	// Cache is valid
	return nil
}

// Delete K8s resources for an HTTP Proxy created for a Microservice
func (mgr *Manager) deleteProxyDeployment() error {
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.namespace,
	}
	meta := metav1.ObjectMeta{
		Name:      proxyName,
		Namespace: mgr.namespace,
	}
	dep := &appsv1.Deployment{ObjectMeta: meta}
	if err := mgr.delete(proxyKey, dep); err != nil {
		return err
	}
	return nil
}

// Delete K8s resources for an HTTP Proxy created for a Microservice
func (mgr *Manager) deleteProxyService() error {
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.namespace,
	}
	meta := metav1.ObjectMeta{
		Name:      proxyName,
		Namespace: mgr.namespace,
	}
	svc := &corev1.Service{ObjectMeta: meta}
	if err := mgr.delete(proxyKey, svc); err != nil {
		return err
	}
	return nil
}

// Create or update an HTTP Proxy instance for a Microservice
func (mgr *Manager) updateProxy() error {
	fmt.Println("Cache reconciled:")
	fmt.Println(mgr.cache)

	// Key to check resources don't already exist
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.namespace,
	}

	// Deployment
	foundDep := appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundDep); err == nil {
		// Existing deployment found, update the proxy configuration
		if err := mgr.updateProxyDeployment(&foundDep); err != nil {
			return err
		}
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Create new secret and deployment
		secret := newProxySecret(mgr.namespace, mgr.routerAddress)
		dep := newProxyDeployment(mgr.namespace, mgr.proxyImage, 1, createProxyConfig(mgr.cache))
		for _, obj := range []metav1.Object{secret, dep} {
			mgr.setOwnerReference(obj)
		}
		for _, obj := range []runtime.Object{secret, dep} {
			if err := mgr.k8sClient.Create(context.TODO(), obj); err != nil {
				return err
			}
		}
	}

	// Service
	foundSvc := corev1.Service{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundSvc); err == nil {
		// Existing service found, update it without touching immutable values
		if err := mgr.updateProxyService(&foundSvc); err != nil {
			return err
		}
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Create new service
		svc := newProxyService(mgr.namespace, mgr.cache)
		mgr.setOwnerReference(svc)
		if err := mgr.k8sClient.Create(context.TODO(), svc); err != nil {
			return err
		}
	}

	return nil
}

func (mgr *Manager) updateProxyService(foundSvc *corev1.Service) error {
	foundSvc.Spec.Ports = make([]corev1.ServicePort, 0)
	for port, queue := range mgr.cache {
		foundSvc.Spec.Ports = append(foundSvc.Spec.Ports, generateServicePort(port, queue))
	}

	// Cannot update service to have 0 ports, delete it
	if len(foundSvc.Spec.Ports) == 0 {
		// Delete empty service
		return mgr.deleteProxyService()
	}

	// Update the service with new ports
	if err := mgr.k8sClient.Update(context.TODO(), foundSvc); err != nil {
		return err
	}

	return nil
}

// TODO: Replace this function with logic to update config in Proxy without editing the deployment
func (mgr *Manager) updateProxyDeployment(foundDep *appsv1.Deployment) error {
	// Generate config
	config := createProxyConfig(mgr.cache)

	if len(config) == 0 {
		// Delete unneeded resource
		return mgr.deleteProxyDeployment()
	}

	// Save the config to deployment
	if err := updateProxyConfig(foundDep, config); err != nil {
		return err
	}

	// Update the deployment
	if err := mgr.k8sClient.Update(context.TODO(), foundDep); err != nil {
		return err
	}
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
