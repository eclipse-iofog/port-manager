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
	"errors"
	"fmt"
	"strconv"
	"strings"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/v2/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func getProxyContainerArgs(config string) []string {
	return []string{
		"node",
		"/opt/app-root/bin/simple.js",
		config,
	}
}
func newProxyDeployment(namespace, name, image string, replicas int32, config, routerHost string) *appsv1.Deployment {
	labels := map[string]string{
		"name": name,
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "proxy",
							Image:           image,
							Args:            getProxyContainerArgs(config),
							ImagePullPolicy: corev1.PullAlways,
							Env: []corev1.EnvVar{
								{
									Name:  "ICPROXY_BRIDGE_HOST",
									Value: routerHost,
								},
							},
						},
					},
				},
			},
		},
	}
}

func getRouterConfig(routerHost string) string { // nolint:unused,deadcode
	config := `{
	"scheme": "amqp",
	"host": "<ROUTER>"
}`
	return strings.Replace(config, "<ROUTER>", routerHost, 1)
}

func newProxyService(namespace, name string, ports portMap, svcType string) *corev1.Service {
	labels := map[string]string{
		"name": name,
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:                  corev1.ServiceType(svcType),
			ExternalTrafficPolicy: getTrafficPolicy(svcType),
			Selector:              labels,
		},
	}
	modifyServiceSpec(svc, ports)

	return svc
}

func createProxyConfig(ports portMap) string {
	config := ""
	for _, port := range ports {
		separator := ","
		if config == "" {
			separator = ""
		}
		config = fmt.Sprintf("%s%s%s", config, separator, createProxyString(port))
	}
	return config
}

func updateProxyConfig(dep *appsv1.Deployment, config string) error {
	if err := checkProxyDeployment(dep); err != nil {
		return err
	}
	dep.Spec.Template.Spec.Containers[0].Args[len(getProxyContainerArgs(""))-1] = config
	return nil
}

func createProxyString(port ioclient.PublicPort) string {
	return fmt.Sprintf("%s:%d=>amqp:%s", port.Protocol, port.Port, port.Queue)
}

func getProxyConfig(dep *appsv1.Deployment) (string, error) {
	if err := checkProxyDeployment(dep); err != nil {
		return "", err
	}
	return dep.Spec.Template.Spec.Containers[0].Args[len(getProxyContainerArgs(""))-1], nil
}

func checkProxyDeployment(dep *appsv1.Deployment) error {
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return errors.New("proxy Deployment has no containers")
	}
	argCount := len(getProxyContainerArgs(""))
	if len(containers[0].Args) != argCount {
		return fmt.Errorf("proxy Deployment argument length is not %d", argCount)
	}
	return nil
}

// Find all ports in config string
func decodeConfig(config, startDelim, endDelim string) (ports map[int]bool, err error) { // nolint:unused,deadcode
	portStrings := between(config, startDelim, endDelim)
	ports = make(map[int]bool)
	for idx := range portStrings {
		port := 0
		port, err = strconv.Atoi(portStrings[idx])
		if err != nil {
			return
		}
		ports[port] = true
	}
	return
}

func decodeMicroservice(configItem string) (*ioclient.PublicPort, error) {
	// {protocol}:{msvcPort}=>amqp:{queueName}
	// Protocol
	protocol := before(configItem, ":")
	if protocol != "http" && protocol != "http2" && protocol != "tcp" {
		return nil, errors.New("Unsupported protocol: " + protocol)
	}
	// Port
	ports := between(configItem, protocol+":", "=>")
	if len(ports) != 1 {
		return nil, errors.New("Could not get port from config item " + configItem)
	}
	port, err := strconv.Atoi(ports[0])
	if err != nil {
		return nil, errors.New("Failed to convert port string to int: " + ports[0])
	}
	// Queue name
	ids := strings.SplitAfter(configItem, "=>amqp:")
	if len(ids) != 2 {
		return nil, errors.New("Could not split after =>amqp: in config item " + configItem)
	}
	queue := ids[1]
	return &ioclient.PublicPort{
		Protocol: protocol,
		Queue:    queue,
		Port:     port,
	}, nil
}

func generateServicePort(port int, queue string) corev1.ServicePort {
	return corev1.ServicePort{
		Name:       strings.ToLower(queue),
		Port:       int32(port),
		TargetPort: intstr.FromInt(port),
		Protocol:   corev1.Protocol("TCP"),
	}
}

func getTrafficPolicy(serviceType string) corev1.ServiceExternalTrafficPolicyType {
	if serviceType == string(corev1.ServiceTypeLoadBalancer) {
		return corev1.ServiceExternalTrafficPolicyTypeLocal
	}
	return ""
}

func modifyServiceSpec(svc *corev1.Service, ports portMap) {
	svc.Spec.Ports = make([]corev1.ServicePort, 0)
	for _, port := range ports {
		svc.Spec.Ports = append(svc.Spec.Ports, generateServicePort(port.Port, port.Queue))
	}
}
