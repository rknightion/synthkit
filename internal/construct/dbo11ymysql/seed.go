// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ymysql

import (
	"fmt"
	"strconv"
)

// masterUUID derives a deterministic MySQL server-UUID (8-4-4-4-12 format)
// from a 64-hex serverID string.
func masterUUID(serverID string) string {
	s := padServerID(serverID, 32)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// masterHost derives a deterministic private IPv4 "10.a.b.c" from serverID.
func masterHost(serverID string) string {
	s := padServerID(serverID, 32)
	a := hexByteModRange(s, 0)
	b := hexByteModRange(s, 2)
	c := hexByteModRange(s, 4)
	return fmt.Sprintf("10.%d.%d.%d", a, b, c)
}

func padServerID(s string, minLen int) string {
	for len(s) < minLen {
		if s == "" {
			s = "0"
		}
		s = s + s
	}
	return s
}

func hexByteModRange(s string, offset int) int {
	v, err := strconv.ParseInt(s[offset:offset+2], 16, 64)
	if err != nil {
		return 1
	}
	return int(v)%254 + 1
}
