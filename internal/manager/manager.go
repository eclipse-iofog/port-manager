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
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/v3/pkg/client"
	waitclient "github.com/eclipse-iofog/iofog-go-sdk/v3/pkg/k8s"

	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Manager struct {
	opt         *Options
	cache       portMap
	k8sClient   k8sclient.Client
	waitClient  *waitclient.Client
	ioClient    *ioclient.Client
	log         logr.Logger
	owner       metav1.OwnerReference
	addressChan chan string
}

type Options struct {
	Namespace            string
	UserEmail            string
	UserPass             string
	ProxyImage           string
	ProxyName            string
	ProxyServiceType     string
	ProtocolFilter       string
	ProxyExternalAddress string
	RouterAddress        string
	Config               *rest.Config
}

func New(opt *Options) (*Manager, error) {
	logf.SetLogger(zap.New())

	password, err := decodeBase64(opt.UserPass)
	if err == nil {
		opt.UserPass = password
	}
	mgr := &Manager{
		cache:       make(portMap),
		log:         logf.Log.WithName(opt.ProxyName),
		opt:         opt,
		addressChan: make(chan string, 5),
	}
	mgr.opt.ProtocolFilter = strings.ToUpper(mgr.opt.ProtocolFilter)
	err = mgr.init()

	return mgr, err
}

// Query the K8s API Server for details of this pod's deployment
// Store details for later use when assigning owners to other K8s resources we make
// Owner reference is required for automatic cleanup of K8s resources made by this runtime
func (mgr *Manager) getOwnerReference() error {
	objKey := k8sclient.ObjectKey{
		Name:      pkg.managerName,
		Namespace: mgr.opt.Namespace,
	}
	dep := appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), objKey, &dep); err != nil {
		return err
	}
	mgr.owner = metav1.OwnerReference{
		APIVersion: "apps/v1",
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
	baseURLStr := fmt.Sprintf("http://%s.%s:%d/api/v3", pkg.controllerServiceName, mgr.opt.Namespace, pkg.controllerPort)
	baseURL, err := url.Parse(baseURLStr)
	if err != nil {
		return fmt.Errorf("could not parse Controller URL %s: %s", baseURLStr, err.Error())
	}
	if mgr.ioClient, err = ioclient.NewAndLogin(ioclient.Options{BaseURL: baseURL}, mgr.opt.UserEmail, mgr.opt.UserPass); err != nil {
		return
	}
	mgr.log.Info("Logged into Controller API")

	// Start address register routine
	go mgr.registerProxyAddress()

	// Check if Proxy Service exists
	svc := corev1.Service{}
	proxyKey := k8sclient.ObjectKey{
		Name:      mgr.opt.ProxyName,
		Namespace: mgr.opt.Namespace,
	}
	// Check if service exists
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &svc); err != nil {
		// Not found, no problem
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	// Service exists, register Service IP
	mgr.addressChan <- mgr.opt.ProxyExternalAddress
	return nil
}

// Main loop of manager
// Query ioFog Controller REST API and compare against cache
// Make updates to K8s resources as required
func (mgr *Manager) Run() {
	// Initialize cache based on K8s API
	if err := mgr.generateCache(); err != nil {
		mgr.log.Error(err, "Failed to generate cache")
		time.Sleep(5 * time.Second)
	}

	// Watch Controller API
	for {
		time.Sleep(pkg.pollInterval)
		if err := mgr.run(); err != nil {
			mgr.log.Error(err, "Failed in watch loop")
		}
	}
}

func (mgr *Manager) generateCache() error {
	mgr.log.Info("Generating cache based on Kubernetes API")
	// Clear the cache
	mgr.cache = make(portMap)

	// Get deployment
	proxyKey := k8sclient.ObjectKey{
		Name:      mgr.opt.ProxyName,
		Namespace: mgr.opt.Namespace,
	}
	foundDep := appsv1.Deployment{}
	if err := mgr.k8sClient.Get(context.TODO(), proxyKey, &foundDep); err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		// Deployment not found, no ports open, nothing to cache
		mgr.log.Info("Initialized with empty cache")
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

	mgr.log.Info("Generated cache", "cache", mgr.cache)
	return nil
}

func (mgr *Manager) run() error {
	cacheReconciled := false

	// Get public ports from Controller
	allBackendPorts, err := mgr.ioClient.GetAllMicroservicePublicPorts()
	if err != nil {
		return err
	}

	var backendPorts []ioclient.MicroservicePublicPort
	// Filter ports based on protocol
	if mgr.opt.ProtocolFilter == "" {
		backendPorts = allBackendPorts
	} else {
		for _, port := range allBackendPorts {
			if strings.EqualFold(port.PublicPort.Protocol, mgr.opt.ProtocolFilter) {
				backendPorts = append(backendPorts, port)
			}
		}
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
		mgr.log.Info("Reconciled cache", "cache", mgr.cache)
		return mgr.updateProxy()
	}

	return nil
}

// Delete K8s resources for an HTTP Proxy created for a Microservice
func (mgr *Manager) deleteProxyDeployment() error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:      mgr.opt.ProxyName,
		Namespace: mgr.opt.Namespace,
	}}
	if err := mgr.delete(dep); err != nil {
		return err
	}
	return nil
}

// Delete K8s resources for an HTTP Proxy created for a Microservice
func (mgr *Manager) deleteProxyService() error {
	// Perform deletion
	proxyKey := k8sclient.ObjectKey{
		Name:      mgr.opt.ProxyName,
		Namespace: mgr.opt.Namespace,
	}
	meta := metav1.ObjectMeta{
		Name:      mgr.opt.ProxyName,
		Namespace: mgr.opt.Namespace,
	}
	svc := &corev1.Service{ObjectMeta: meta}
	if err := mgr.delete(svc); err != nil {
		return err
	}
	// Wait for service to be gone
	timeout := time.Second * 60
	for start := time.Now(); time.Since(start) < timeout; {
		if err := mgr.k8sClient.Get(context.Background(), proxyKey, svc); err != nil {
			// Not found, deletion complete
			if k8serrors.IsNotFound(err) {
				return nil
			}
			// Another error occurred
			return err
		}
		time.Sleep(time.Second * 2)
	}
	return errors.New("timed out waiting for Proxy Service deletion")
}

// Create or update an HTTP Proxy instance for a Microservice
func (mgr *Manager) updateProxy() error {
	// Key to check resources don't already exist
	proxyKey := k8sclient.ObjectKey{
		Name:      mgr.opt.ProxyName,
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
		dep := newProxyDeployment(mgr.opt.Namespace, mgr.opt.ProxyName, mgr.opt.ProxyImage, 1, createProxyConfig(mgr.cache), mgr.opt.RouterAddress)
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
		// Create new service if ports exist
		svc := newProxyService(mgr.opt.Namespace, mgr.opt.ProxyName, mgr.cache, mgr.opt.ProxyServiceType)
		mgr.setOwnerReference(svc)
		if err := mgr.k8sClient.Create(context.TODO(), svc); err != nil {
			return err
		}
		// Trigger address registration for Controller
		mgr.addressChan <- mgr.opt.ProxyExternalAddress
	}

	return nil
}

func (mgr *Manager) registerProxyAddress() {
	timeout := int64(60)
	var err error

	for {
		// Wait for signal
		addr := <-mgr.addressChan

		if addr == "" {
			// Wait for LB Service
			addr, err = mgr.waitClient.WaitForLoadBalancer(mgr.opt.Namespace, mgr.opt.ProxyName, timeout)
			if err != nil {
				mgr.log.Error(err, "Failed to find IP address of Proxy Service")
				// Wait
				time.Sleep(5 * time.Second)
				// Retry
				mgr.addressChan <- ""
				continue
			}
		}

		// Attempt to register
		err = mgr.ioClient.PutDefaultProxy(addr)
		if err != nil {
			mgr.log.Error(err, "Failed to register Proxy address "+addr)
			// Wait
			time.Sleep(5 * time.Second)
			// Retry with LB addr
			mgr.addressChan <- addr
			continue
		}

		mgr.log.Info("Successfully registered Proxy address " + addr)
	}
}

func (mgr *Manager) updateProxyService(foundSvc *corev1.Service) error {
	modifyServiceSpec(foundSvc, mgr.cache)

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

	if config == "" {
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

func (mgr *Manager) delete(obj k8sclient.Object) error {
	if err := mgr.k8sClient.Delete(context.Background(), obj); err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		return err
	}
	return nil
}

func (mgr *Manager) setOwnerReference(obj metav1.Object) {
	obj.SetOwnerReferences([]metav1.OwnerReference{mgr.owner})
}
