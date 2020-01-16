package manager

import (
	"fmt"

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

func createProxyConfig(msvc *ioclient.MicroserviceInfo) string {
	config := ""
	for idx, msvcPort := range msvc.Ports {
		separator := ""
		if idx != 0 {
			separator = ","
		}
		config = fmt.Sprintf("%s%s%s", config, separator, createProxyString(msvc.Name, msvc.UUID, msvcPort.External))
	}
	return config
}

func createProxyString(msvcName, msvcUUID string, msvcPort int) string {
	return fmt.Sprintf("http:%d=>amqp:%s-%s", msvcPort, msvcName, msvcUUID)
}
