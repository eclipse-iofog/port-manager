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
	"testing"

	ioclient "github.com/eclipse-iofog/iofog-go-sdk/pkg/client"
)

func TestProxyString(t *testing.T) {
	port := ioclient.PublicPort{
		Queue: "W6R2RFNBgTYnLtLkQ6yCDDv979QLhFXb",
		Port: 5000,
		Protocol: "tcp",
	}

	config := createProxyString(port)
	if config != "tcp:5000=>amqp:W6R2RFNBgTYnLtLkQ6yCDDv979QLhFXb" {
		t.Errorf("Failed to create Proxy string")
	}
}
