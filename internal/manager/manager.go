/*
 *  *******************************************************************************
 *  * Copyright (c) 2019 Edgeworx, Inc.
 *  *
 *  * This program and the accompanying materials are made available under the
 *  * terms of the Eclipse Public License v. 2.0 which is available at
 *  * http://www.eclipse.org/legal/epl-2.0
 *  *
 *  * SPDX-License-Identifier: EPL-2.0
 *  *******************************************************************************
 *
 */

package manager

import (
	"context"
	"encoding/json"
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

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/v2/pkg/client"
	waitclient "github.com/eclipse-iofog/iofog-go-sdk/v2/pkg/k8s"

	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const (
	controllerServiceName = "controller"
	controllerPort        = 51121
	managerName           = "port-manager"
	pollInterval          = time.Second * 10
)

type portMap map[int]ioclient.PublicPort // Indexed by port

type Manager struct {
	opt            Options
	iofogUserEmail string
	iofogUserPass  string
	cache          portMap
	k8sClient      k8sclient.Client
	waitClient     *waitclient.Client
	ioClient       *ioclient.Client
	log            logr.Logger
	owner          metav1.OwnerReference
	proxyImage     string
	routerAddress  string
	addressChan    chan interface{}
}

type Options struct {
	Namespace        string
	UserEmail        string
	UserPass         string
	ProxyImage       string
	ProxyServiceType string
	ProxyAddress     string
	RouterAddress    string
	Config           *rest.Config
}

func New(opt Options) (*Manager, error) {
	logf.SetLogger(logf.ZapLogger(false))

	password, err := decodeBase64(opt.UserPass)
	if err == nil {
		opt.UserPass = password
	}
	mgr := &Manager{
		cache:       make(portMap),
		log:         logf.Log.WithName(managerName),
		opt:         opt,
		addressChan: make(chan interface{}, 5),
	}
	return mgr, mgr.init()
}

// Query the K8s API Server for details of this pod's deployment
// Store details for later use when assigning owners to other K8s resources we make
// Owner reference is required for automatic cleanup of K8s resources made by this runtime
func (mgr *Manager) getOwnerReference() error {
	objKey := k8sclient.ObjectKey{
		Name:      managerName,
		Namespace: mgr.opt.Namespace,
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

func (mgr *Manager) init() (err error) {
	// Instantiate Kubernetes client
	if mgr.k8sClient, err = k8sclient.New(mgr.opt.Config, k8sclient.Options{}); err != nil {
		return
	}
	if mgr.waitClient, err = waitclient.NewInCluster(); err != nil {
		return
	}
	mgr.log.Info("Created Kubernetes clients")

	// Get owner reference
	if err = mgr.getOwnerReference(); err != nil {
		return
	}
	mgr.log.Info("Got owner reference from Kubernetes API Server")

	// Set up ioFog client
	ioclient.SetGlobalRetries(ioclient.Retries{
		CustomMessage: map[string]int{
			"timeout":    10,
			"refuse":     10,
			"credential": 10,
		},
	})
	controllerEndpoint := fmt.Sprintf("%s.%s:%d", controllerServiceName, mgr.opt.Namespace, controllerPort)
	if mgr.ioClient, err = ioclient.NewAndLogin(ioclient.Options{Endpoint: controllerEndpoint}, mgr.opt.UserEmail, mgr.opt.UserPass); err != nil {
		return
	}
	mgr.log.Info("Logged into Controller API")

	// Start address register routine
	go mgr.registerProxyAddress()

	// Check if Proxy Service exists
	svc := corev1.Service{}
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.opt.Namespace,
	}
	if svcErr := mgr.k8sClient.Get(context.TODO(), proxyKey, &svc); svcErr == nil {
		// Register Service IP
		mgr.addressChan <- nil
	}

	return
}

// Main loop of manager
// Query ioFog Controller REST API and compare against cache
// Make updates to K8s resources as required
func (mgr *Manager) Run() (err error) {
	// Initialize cache based on K8s API
	if err := mgr.generateCache(); err != nil {
		return err
	}

	// Watch Controller API
	for {
		time.Sleep(pollInterval)
		if err := mgr.run(); err != nil {
			return err
		}
	}
}

func (mgr *Manager) printCache() {
	cache, _ := json.MarshalIndent(mgr.cache, "", "\t")
	fmt.Println(string(cache))
}

func (mgr *Manager) generateCache() error {
	mgr.log.Info("Generating cache based on Kubernetes API")
	// Clear the cache
	mgr.cache = make(portMap)

	// Get deployment
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.opt.Namespace,
	}
	foundDep := appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundDep); err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Deployment not found, no ports open, nothing to cache
		mgr.printCache()
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
		port, err := decodeMicroservice(configItem)
		if err != nil {
			return err
		}
		// Update cache
		mgr.cache[port.Port] = *port
	}

	fmt.Println(mgr.cache)
	return nil
}

func (mgr *Manager) run() error {
	cacheReconciled := false

	// Check ports
	backendPorts, err := mgr.ioClient.GetAllMicroservicePublicPorts()
	if err != nil {
		return err
	}

	// Update Proxy config if new ports are created or queues changed
	for _, backendPort := range backendPorts {
		newPort := backendPort.PublicPort
		existingPort, exists := mgr.cache[newPort.Port]
		// Microservice already stored in cache
		if exists {
			// Check for queue change
			if existingPort.Queue != newPort.Queue || existingPort.Protocol != newPort.Protocol {
				cacheReconciled = true
				// Update cache
				mgr.cache[newPort.Port] = newPort
			}
		} else {
			// New port, update cache
			cacheReconciled = true
			mgr.cache[newPort.Port] = newPort
		}
	}

	// Update Proxy config if ports are deleted
	// Create map of backend ports
	backendPortMap := make(map[int]string)
	for _, backendPort := range backendPorts {
		backendPortMap[backendPort.PublicPort.Port] = backendPort.PublicPort.Queue
	}
	for port := range mgr.cache {
		// Cached port does not exist in backend, delete it
		if _, exists := backendPortMap[port]; !exists {
			// Cached microservice not found in backend
			cacheReconciled = true
			// Remove microservice from cache
			delete(mgr.cache, port)
		}
	}

	// Update K8s resources
	if cacheReconciled {
		fmt.Println("Cache reconciled:")
		fmt.Println(mgr.cache)
		return mgr.updateProxy()
	}

	return nil
}

// Delete K8s resources for an HTTP Proxy created for a Microservice
func (mgr *Manager) deleteProxyDeployment() error {
	// Dep
	key := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.opt.Namespace,
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:      proxyName,
		Namespace: mgr.opt.Namespace,
	}}
	if err := mgr.delete(key, dep); err != nil {
		return err
	}
	return nil
}

// Delete K8s resources for an HTTP Proxy created for a Microservice
func (mgr *Manager) deleteProxyService() error {
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.opt.Namespace,
	}
	meta := metav1.ObjectMeta{
		Name:      proxyName,
		Namespace: mgr.opt.Namespace,
	}
	svc := &corev1.Service{ObjectMeta: meta}
	if err := mgr.delete(proxyKey, svc); err != nil {
		return err
	}
	return nil
}

// Create or update an HTTP Proxy instance for a Microservice
func (mgr *Manager) updateProxy() error {
	// Key to check resources don't already exist
	proxyKey := k8sclient.ObjectKey{
		Name:      proxyName,
		Namespace: mgr.opt.Namespace,
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
		// Create new deployment
		dep := newProxyDeployment(mgr.opt.Namespace, mgr.opt.ProxyImage, 1, createProxyConfig(mgr.cache), mgr.opt.RouterAddress)
		mgr.setOwnerReference(dep)
		if err := mgr.k8sClient.Create(context.TODO(), dep); err != nil {
			return err
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
		svc := newProxyService(mgr.opt.Namespace, mgr.cache, mgr.opt.ProxyServiceType, mgr.opt.ProxyAddress)
		mgr.setOwnerReference(svc)
		if err := mgr.k8sClient.Create(context.TODO(), svc); err != nil {
			return err
		}
		mgr.addressChan <- nil
	}

	return nil
}

func (mgr *Manager) registerProxyAddress() {
	timeout := int64(60)
	for {
		// Wait for signal
		<-mgr.addressChan
		// Get Service address
		ip, err := mgr.waitClient.WaitForLoadBalancer(mgr.opt.Namespace, proxyName, timeout)
		if err != nil {
			mgr.log.Error(err, "Failed to find IP address of Proxy Service")
			// Wait
			time.Sleep(5 * time.Second)
			// Retry
			mgr.addressChan <- nil
			continue
		}

		// Attempt to register
		err = mgr.ioClient.PutDefaultProxy(ip)
		if err != nil {
			mgr.log.Error(err, "Failed to register Proxy address "+ip)
			// Wait
			time.Sleep(5 * time.Second)
			// Retry
			mgr.addressChan <- nil
			continue
		}

		mgr.log.Info("Successfully registered Proxy address " + ip)
	}
}

func (mgr *Manager) updateProxyService(foundSvc *corev1.Service) error {
	foundSvc.Spec.Ports = make([]corev1.ServicePort, 0)
	for _, port := range mgr.cache {
		foundSvc.Spec.Ports = append(foundSvc.Spec.Ports, generateServicePort(port.Port, port.Queue))
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
