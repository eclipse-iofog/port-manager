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

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"
)

func newProxyDeployment(namespace, msvcName, config string, image string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{
		"name": getProxyName(msvcName),
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getProxyName(msvcName),
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
								"/opt/app-root/bin/icproxy.js",
								config,
							},
							//Args:
							//Ports:
							//Env: []corev1.EnvVar{
							//	{
							//		Name: "ICPROXY_CONFIG",
							//	},
							//},
							//Resources:
							//ReadinessProbe:  &corev1.Probe{},
							ImagePullPolicy: corev1.PullAlways,
						},
					},
				},
			},
		},
	}
}

func newProxyService(namespace, msvcName string, msvcPorts []ioclient.MicroservicePortMapping) *corev1.Service {
	labels := map[string]string{
		"name": getProxyName(msvcName),
	}
	ports := make([]corev1.ServicePort, 0)
	for _, msvcPort := range msvcPorts {
		ports = append(ports, corev1.ServicePort{
			Name:       "proxy",
			Port:       int32(msvcPort.External),
			TargetPort: intstr.FromInt(msvcPort.External),
			Protocol:   corev1.Protocol("TCP"),
		})
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getProxyName(msvcName),
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

func getProxyName(msvcName string) string {
	return fmt.Sprintf("%s-http-proxy", msvcName)
}

func createProxyConfig(msvcName, msvcUUID string, msvcPort int) string {
	return fmt.Sprintf("http:%d=>amqp:%s-%s", msvcPort, msvcName, msvcUUID)
}

func getProxyConfig(dep *appsv1.Deployment) (string, error) {
	if err := checkProxyDeployment(dep); err != nil {
		return "", err
	}
	return dep.Spec.Template.Spec.Containers[0].Command[2], nil
}

func updateProxyConfig(dep *appsv1.Deployment, config string) error {
	if err := checkProxyDeployment(dep); err != nil {
		return err
	}
	dep.Spec.Template.Spec.Containers[0].Command[2] = config
	return nil
}

func checkProxyDeployment(dep *appsv1.Deployment) error {
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return errors.New("Proxy Deployment has no containers")
	}
	commands := containers[0].Command
	if len(commands) != 3 {
		return errors.New("Proxy Deployment command length is not 3")
	}
	return nil
}

func decodeConfig(config string) (ports []int, err error) {
	portStrings := between(config, "http:", "=>")
	ports = make([]int, len(portStrings))
	for idx := range portStrings {
		port := 0
		port, err = strconv.Atoi(portStrings[idx])
		if err != nil {
			return
		}
		ports = append(ports, port)
	}
	return
}

func between(value string, a string, b string) (substrs []string) {
	substrs = make([]string, 0)
	iter := 0
	for iter < len(value)+1 {
		posFirst := strings.Index(value, a)
		if posFirst == -1 {
			return
		}
		posLast := strings.Index(value, b)
		if posLast == -1 {
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
