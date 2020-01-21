package manager

import (
	"fmt"
	"testing"
)

func TestPortDecode(t *testing.T) {
	msvcName := "heart-rate-viewer"
	msvcUUID := "W6R2RFNBgTYnLtLkQ6yCDDv979QLhFXb"
	msvcPort := 5000
	config := createProxyString(msvcName, msvcUUID, msvcPort)
	ports, err := decodePorts(config, msvcName, msvcUUID)
	if err != nil {
		t.Errorf(err.Error())
	}
	if len(ports) != 1 {
		t.Errorf(fmt.Sprintf("Incorrect number of ports: %d", len(ports)))
	}
	if _, exists := ports[msvcPort]; !exists {
		t.Errorf(fmt.Sprintf("Could not find port %d", msvcPort))
	}
	msvcName = msvcName + "2"
	msvcUUID = "ud32iu23bois90ahdiaojkda"
	msvcPort = 6000
	config = fmt.Sprintf("%s,%s", config, createProxyString(msvcName, msvcUUID, msvcPort))
	ports, err = decodePorts(config, msvcName, msvcUUID)
	if err != nil {
		t.Errorf(err.Error())
	}
	if len(ports) != 1 {
		t.Errorf(fmt.Sprintf("Incorrect number of ports: %d", len(ports)))
	}
	if _, exists := ports[msvcPort]; !exists {
		t.Errorf(fmt.Sprintf("Could not find port %d", msvcPort))
	}
}
