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
	"strings"
)

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

func before(input string, substr string) string {
	pos := strings.Index(input, substr)
	if pos == -1 {
		return input
	}
	return input[0:pos]
}

func after(input string, substr string) string {
	pos := strings.Index(input, substr)
	if pos == -1 || pos >= len(input)-1 {
		return ""
	}
	return input[pos+1:]
}
