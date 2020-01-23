package manager

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"
)

const (
	proxyName = "http-proxy"
)

func newProxyDeployment(namespace, config string, image string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{
		"name": proxyName,
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyName,
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
							Name:  "proxy",
							Image: image,
							Command: []string{
								"node",
							},
							Args: []string{
								"/opt/app-root/bin/simple.js",
								config,
							},
							ImagePullPolicy: corev1.PullAlways,
						},
					},
				},
			},
		},
	}
}

func newProxyService(namespace string, msvcs []*ioclient.MicroserviceInfo) *corev1.Service {
	labels := map[string]string{
		"name": proxyName,
	}
	ports := make([]corev1.ServicePort, 0)
	for _, msvc := range msvcs {
		ports = append(ports, generateServicePorts(msvc.Name, msvc.UUID, msvc.Ports)...)
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:                  corev1.ServiceTypeLoadBalancer,
			ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal,
			Selector:              labels,
			Ports:                 ports,
		},
	}
}

func createProxyConfig(msvcs []*ioclient.MicroserviceInfo) string {
	config := ""
	for msvcIdx, msvc := range msvcs {
		for _, msvcPort := range msvc.Ports {
			if msvcPort.External == 0 {
				continue
			}
			separator := ""
			if msvcIdx != 0 {
				separator = ","
			}
			config = fmt.Sprintf("%s%s%s", config, separator, createProxyString(msvc.Name, msvc.UUID, msvcPort.External))
		}
	}
	return config
}

func updateProxyConfig(dep *appsv1.Deployment, config string) error {
	if err := checkProxyDeployment(dep); err != nil {
		return err
	}
	dep.Spec.Template.Spec.Containers[0].Args[1] = config
	return nil
}

func createProxyString(msvcName, msvcUUID string, msvcPort int) string {
	return fmt.Sprintf("http:%d=>amqp:%s-%s", msvcPort, msvcName, msvcUUID)
}

func getProxyConfig(dep *appsv1.Deployment) (string, error) {
	if err := checkProxyDeployment(dep); err != nil {
		return "", err
	}
	return dep.Spec.Template.Spec.Containers[0].Args[1], nil
}

func checkProxyDeployment(dep *appsv1.Deployment) error {
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return errors.New("Proxy Deployment has no containers")
	}
	if len(containers[0].Args) != 2 {
		return errors.New("Proxy Deployment argument length is not 2")
	}
	return nil
}

// Find all ports in config string
func decodeConfig(config, startDelim, endDelim string) (ports map[int]bool, err error) {
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

func decodeAllPorts(config string) (ports map[int]bool, err error) {
	return decodeConfig(config, "http:", "=>")
}

func decodePorts(config, msvcName, msvcUUID string) (ports map[int]bool, err error) {
	return decodeConfig(config, "http:", fmt.Sprintf("=>amqp:%s-%s", msvcName, msvcUUID))
}

func decodeMicroservice(configItem string) (msvcPort int, msvcName, msvcUUID string, err error) {
	// http:{msvcPort}=>amqp:{msvcName-msvcUUID}
	// Port
	ports := between(configItem, "http:", "=>")
	if len(ports) != 1 {
		err = errors.New("Could not get port from config item " + configItem)
		return
	}
	msvcPort, err = strconv.Atoi(ports[0])
	if err != nil {
		err = errors.New("Failed to convert port string to int: " + ports[0])
		return
	}
	// Name and UUID
	ids := strings.SplitAfter(configItem, "=>amqp:")
	if len(ids) != 2 {
		err = errors.New("Could not split after =>amqp: in config item " + configItem)
		return
	}
	id := ids[1]
	separatorIdx := strings.LastIndex(id, "-")
	if separatorIdx == -1 || separatorIdx >= len(id)-1 {
		err = errors.New("Could not find last index of - char in config item " + configItem)
		return
	}
	msvcName = id[:separatorIdx]
	msvcUUID = id[separatorIdx+1:]
	return
}

// Find all substrings between a and b until end
func between(value string, a string, b string) (substrs []string) {
	substrs = make([]string, 0)
	iter := 0
	for iter < len(value)+1 {
		posLast := strings.Index(value, b)
		if posLast == -1 {
			return
		}
		posFirst := strings.LastIndex(value[:posLast], a)
		if posFirst == -1 {
			return
		}
		posFirstAdjusted := posFirst + len(a)
		if posFirstAdjusted >= posLast {
			return
		}
		substrs = append(substrs, value[posFirstAdjusted:posLast])
		value = value[posLast+1:]
	}
	return
}
