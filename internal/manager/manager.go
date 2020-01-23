package manager

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	proxyImage            = "quay.io/skupper/icproxy"
	managerName           = "port-manager"
	pollInterval          = time.Second * 10
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

	// Initialize cache based on K8s API
	if err := mgr.generateCache(ioClient); err != nil {
		return err
	}

	// Watch Controller API
	for {
		time.Sleep(pollInterval)
		if err := mgr.run(ioClient); err != nil {
			mgr.log.Error(err, "Run loop failed")
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
		// http:{msvcPort}=>amqp:{msvcName-msvcUUID}
		// Port
		ports := between(configItem, "http:", "=>")
		if len(ports) != 1 {
			return errors.New("Could not get port from config item " + configItem)
		}
		msvcPort, err := strconv.Atoi(ports[0])
		if err != nil {
			return errors.New("Failed to convert port string to int: " + ports[0])
		}
		// Name and UUID
		ids := strings.SplitAfter(configItem, "=>amqp:")
		if len(ids) != 2 {
			return errors.New("Could not split after =>amqp: in config item " + configItem)
		}
		id := ids[1]
		separatorIdx := strings.LastIndex(id, "-")
		if separatorIdx == -1 || separatorIdx >= len(id)-1 {
			return errors.New("Could not find last index of - char in config item " + configItem)
		}
		msvcName := id[:separatorIdx]
		msvcUUID := id[separatorIdx+1:]

		// Store microservices to cache (name, uuid, ports)
		portMapping := ioclient.MicroservicePortMapping{
			External: msvcPort,
		}
		// Update cache
		if cachedMsvc, exists := mgr.msvcCache[msvcUUID]; exists {
			cachedMsvc.Ports = append(cachedMsvc.Ports, portMapping)
		} else {
			mgr.msvcCache[msvcUUID] = &ioclient.MicroserviceInfo{
				UUID: msvcUUID,
				Name: msvcName,
				Ports: []ioclient.MicroservicePortMapping{
					portMapping,
				},
			}
		}
	}
	return nil
}

func (mgr *Manager) run(ioClient *ioclient.Client) error {
	mgr.log.Info("Polling Controller API")
	// Check ports
	msvcs, err := ioClient.GetAllMicroservices()
	if err != nil {
		return err
	}
	mgr.log.Info(fmt.Sprintf("Found %d Microservices", len(msvcs.Microservices)))

	changedMsvcs := make([]*ioclient.MicroserviceInfo, 0)
	// Create/update resources based on microservice port state
	for _, msvc := range msvcs.Microservices {
		_, exists := mgr.msvcCache[msvc.UUID]
		if exists {
			// Microservice already stored in cache
			if mgr.cacheIsInvalid(msvc) {
				changedMsvcs = append(changedMsvcs, &msvc)
			}
		} else {
			// Microservice not stored in cache
			if hasPublicPorts(msvc) {
				mgr.log.Info("Found Microservice that is not cached", "Microservice", msvc.Name)
				changedMsvcs = append(changedMsvcs, &msvc)
			}
		}
	}

	// Delete resources for erased microservices
	// Build map to avoid O(N^2) time complexity where N is msvc count
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
		mgr.log.Info("Deleting Microservice from cache", "Microservice", cachedMsvc.Name)
		// Cached microservice not found in backend
		// Delete resources from K8s API Server
		cachedMsvc.Ports = make([]ioclient.MicroservicePortMapping, 0)
		changedMsvcs = append(changedMsvcs, cachedMsvc)
		// Remove microservice from cache
		delete(mgr.msvcCache, cachedMsvc.UUID)
	}

	if err = mgr.updateProxy(changedMsvcs); err != nil {
		return err
	}

	// Update cache
	for _, changedMsvc := range changedMsvcs {
		if len(changedMsvc.Ports) == 0 {
			delete(mgr.msvcCache, changedMsvc.UUID)
		} else {
			mgr.msvcCache[changedMsvc.UUID] = changedMsvc
		}
	}

	return nil
}

// Update K8s resources for a Microservice found in this runtime's cache
func (mgr *Manager) cacheIsInvalid(msvc ioclient.MicroserviceInfo) bool {
	mgr.log.Info("Handling cached Microservice", "Microservice", msvc.Name)
	// Find any newly added ports
	// Build map to avoid O(N^2) time complexity where N is msvc port count
	cachedPorts := buildPortMap(mgr.msvcCache[msvc.UUID].Ports)
	for _, msvcPort := range msvc.Ports {
		if _, exists := cachedPorts[msvcPort.External]; !exists {
			// Make updates with K8s API Server
			return true
		}
	}
	// Find any removed ports
	// Build map to avoid O(N^2) time complexity where N is msvc port count
	backendPorts := buildPortMap(msvc.Ports)
	for cachedPort := range cachedPorts {
		if _, exists := backendPorts[cachedPort]; !exists {
			// Did not find cached port in backend, delete cached port
			// Make updates with K8s API Server
			return true
		}
	}

	return false
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
func (mgr *Manager) updateProxy(msvcs []*ioclient.MicroserviceInfo) error {
	if len(msvcs) == 0 {
		return nil
	}

	// Key to check resources don't already exist
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.namespace,
	}

	// Deployment
	foundDep := appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundDep); err == nil {
		// Existing deployment found, update the proxy configuration
		if err := mgr.updateProxyDeployment(&foundDep, msvcs); err != nil {
			return err
		}
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Create new deployment
		dep := newProxyDeployment(mgr.namespace, createProxyConfig(msvcs), proxyImage, 1)
		mgr.setOwnerReference(dep)
		if err := mgr.k8sClient.Create(context.TODO(), dep); err != nil {
			return err
		}
	}

	// Service
	foundSvc := corev1.Service{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundSvc); err == nil {
		// Existing service found, update it without touching immutable values
		if err := mgr.updateProxyService(&foundSvc, msvcs); err != nil {
			return err
		}
	} else {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Create new service
		svc := newProxyService(mgr.namespace, msvcs)
		mgr.setOwnerReference(svc)
		if err := mgr.k8sClient.Create(context.TODO(), svc); err != nil {
			return err
		}
	}

	return nil
}

func (mgr *Manager) updateProxyService(foundSvc *corev1.Service, msvcs []*ioclient.MicroserviceInfo) error {
	for _, msvc := range msvcs {
		// Get service ports pertaining to this microservice
		svcPorts := getServicePorts(msvc.Name, msvc.UUID, foundSvc.Spec.Ports)
		// Add new ports that don't appear in service
		for idx, msvcPort := range msvc.Ports {
			if msvcPort.External != 0 {
				if _, exists := svcPorts[msvcPort.External]; !exists {
					svcPorts[msvcPort.External] = generateServicePort(msvc.Name, msvc.UUID, msvcPort.External, idx)
				}
			}
		}
		// Remove old ports that appear in service
		msvcPorts := buildPortMap(msvc.Ports)
		for _, svcPort := range svcPorts {
			if _, exists := msvcPorts[int(svcPort.Port)]; !exists {
				delete(svcPorts, int(svcPort.Port))
			}
		}

		// Remove existing ports
		for idx, svcPort := range foundSvc.Spec.Ports {
			if strings.Contains(svcPort.Name, generateServicePortPrefix(msvc.Name, msvc.UUID)) {
				foundSvc.Spec.Ports = append(foundSvc.Spec.Ports[0:idx], foundSvc.Spec.Ports[idx+1:]...)
			}
		}
		// Save the new ports
		for _, svcPort := range svcPorts {
			foundSvc.Spec.Ports = append(foundSvc.Spec.Ports, svcPort)
		}
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
func (mgr *Manager) updateProxyDeployment(foundDep *appsv1.Deployment, msvcs []*ioclient.MicroserviceInfo) error {
	config, err := getProxyConfig(foundDep)
	if err != nil {
		return err
	}
	// Record config to check for changes later
	existingConfig := config

	for _, msvc := range msvcs {
		configPorts, err := decodePorts(config, msvc.Name, msvc.UUID)
		if err != nil {
			return err
		}

		// Add new ports that don't appear in config
		for _, msvcPort := range msvc.Ports {
			if msvcPort.External != 0 {
				if _, exists := configPorts[msvcPort.External]; !exists {
					separator := ","
					if config == "" {
						separator = ""
					}
					config = fmt.Sprintf("%s%s%s", config, separator, createProxyString(msvc.Name, msvc.UUID, msvcPort.External))
				}
			}
		}

		// Remove old ports that appear in config
		msvcPorts := buildPortMap(msvc.Ports)
		for configPort := range configPorts {
			if _, exists := msvcPorts[configPort]; !exists {
				rmvSubstr := createProxyString(msvc.Name, msvc.UUID, configPort)
				config = strings.Replace(config, ","+rmvSubstr, "", 1)
				config = strings.Replace(config, rmvSubstr, "", 1)
			}
		}
	}
	if len(config) == 0 {
		// Delete unneeded resource
		return mgr.deleteProxyDeployment()
	}

	// Remove leading comma
	if config[0] == ',' {
		config = config[1:]
	}

	// No changes to config, don't update
	if config == existingConfig {
		return nil
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
