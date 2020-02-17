package manager

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	proxyName   = "http-proxy"
	proxySecret = "proxy-router-config"
)

func getProxyContainerArgs(config string) []string {
	return []string{
		"node",
		"/opt/app-root/bin/simple.js",
		config,
	}
}
func newProxyDeployment(namespace, image string, replicas int32, config string) *appsv1.Deployment {
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
					Volumes: []corev1.Volume{
						{
							Name: proxySecret,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: proxySecret,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "proxy",
							Image:           image,
							Args:            getProxyContainerArgs(config),
							ImagePullPolicy: corev1.PullAlways,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      proxySecret,
									MountPath: "/etc/messaging",
								},
							},
						},
					},
				},
			},
		},
	}
}

func newProxySecret(namespace, routerHost string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxySecret,
			Namespace: namespace,
		},
		StringData: map[string]string{
			"connect.json": getRouterConfig(routerHost),
		},
	}
}

func getRouterConfig(routerHost string) string {
	config := `{
	"scheme": "amqp",
	"host": "<ROUTER>"
}`
	return strings.Replace(config, "<ROUTER>", routerHost, 1)
}

func newProxyService(namespace string, ports portMap) *corev1.Service {
	labels := map[string]string{
		"name": proxyName,
	}
	svcPorts := make([]corev1.ServicePort, 0)
	for port, queue := range ports {
		svcPorts = append(svcPorts, generateServicePort(port, queue))
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
			Ports:                 svcPorts,
		},
	}
}

func createProxyConfig(ports portMap) string {
	config := ""
	for port, queue := range ports {
		separator := ","
		if config == "" {
			separator = ""
		}
		config = fmt.Sprintf("%s%s%s", config, separator, createProxyString(port, queue))
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

func createProxyString(port int, queue string) string {
	return fmt.Sprintf("http:%d=>amqp:%s", port, queue)
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
	argCount := len(getProxyContainerArgs(""))
	if len(containers[0].Args) != argCount {
		return errors.New(fmt.Sprintf("Proxy Deployment argument length is not %d", argCount))
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

func decodeMicroservice(configItem string) (port int, queue string, err error) {
	// http:{msvcPort}=>amqp:{queueName}
	// Port
	ports := between(configItem, "http:", "=>")
	if len(ports) != 1 {
		err = errors.New("Could not get port from config item " + configItem)
		return
	}
	port, err = strconv.Atoi(ports[0])
	if err != nil {
		err = errors.New("Failed to convert port string to int: " + ports[0])
		return
	}
	// Queue name
	ids := strings.SplitAfter(configItem, "=>amqp:")
	if len(ids) != 2 {
		err = errors.New("Could not split after =>amqp: in config item " + configItem)
		return
	}
	queue = ids[1]
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

func generateServicePort(port int, queue string) corev1.ServicePort {
	return corev1.ServicePort{
		Name:       strings.ToLower(queue),
		Port:       int32(port),
		TargetPort: intstr.FromInt(port),
		Protocol:   corev1.Protocol("TCP"),
	}
}
