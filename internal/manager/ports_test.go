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
