package manager

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"
)

func hasPublicPorts(msvc ioclient.MicroserviceInfo) bool {
	for _, msvcPort := range msvc.Ports {
		if msvcPort.Public != 0 {
			return true
		}
	}
	return false
}

func buildPortMap(ports []ioclient.MicroservicePortMapping) map[int]bool {
	portMap := make(map[int]bool)
	for _, port := range ports {
		if port.Public != 0 {
			portMap[port.Public] = true
		}
	}
	return portMap
}

func generateServicePorts(msvcName, msvcUUID string, msvcPorts []ioclient.MicroservicePortMapping) []corev1.ServicePort {
	ports := make([]corev1.ServicePort, 0)
	for idx, msvcPort := range msvcPorts {
		if msvcPort.Public == 0 {
			continue
		}
		ports = append(ports, generateServicePort(msvcName, msvcUUID, msvcPort.Public, idx))
	}
	return ports
}

func generateServicePort(msvcName, msvcUUID string, msvcPort, index int) corev1.ServicePort {
	return corev1.ServicePort{
		Name:       fmt.Sprintf("%s-%d", generateServicePortPrefix(msvcName, msvcUUID), index),
		Port:       int32(msvcPort),
		TargetPort: intstr.FromInt(msvcPort),
		Protocol:   corev1.Protocol("TCP"),
	}
}

func getServicePorts(msvcName, msvcUUID string, allPorts []corev1.ServicePort) map[int]corev1.ServicePort {
	result := make(map[int]corev1.ServicePort)
	for _, port := range allPorts {
		if strings.Contains(port.Name, generateServicePortPrefix(msvcName, msvcUUID)) {
			result[int(port.Port)] = port
		}
	}
	return result
}

func generateServicePortPrefix(msvcName, msvcUUID string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(msvcName), strings.ToLower(msvcUUID))
}
