// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"net"
	"os"
	"sync/atomic"
	"testing"
)

// childHTTPAddr is the control address a test gives a child process. The child
// binds an ephemeral loopback port itself. Reserving a port in the parent,
// closing it and handing it to the child leaves a window in which another
// process can take it (the child fails to bind, or the parent's probes reach a
// foreign server).
const childHTTPAddr = "127.0.0.1:0"

// childHTTPAddrFileEnv names the file in which a helper publishes the address its
// control listener actually bound. Unset, nothing is published.
const childHTTPAddrFileEnv = "SYNTHKIT_TEST_HTTP_ADDR_FILE"

// childBoundAddr is the bound control address, read by code running in the helper
// process itself.
var childBoundAddr atomic.Pointer[string]

// recordBoundHTTPAddr installs the test-only WrapHTTPListener hook in a helper
// process, chaining any hook already set. It records the bound address and, when
// childHTTPAddrFileEnv is set, publishes it atomically so a parent never reads a
// partial address.
func recordBoundHTTPAddr(deps *haDependencies) {
	next := deps.WrapHTTPListener
	deps.WrapHTTPListener = func(l net.Listener) net.Listener {
		if next != nil {
			l = next(l)
		}
		addr := l.Addr().String()
		childBoundAddr.Store(&addr)
		if path := os.Getenv(childHTTPAddrFileEnv); path != "" {
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(addr), 0600); err != nil {
				panic(err)
			}
			if err := os.Rename(tmp, path); err != nil {
				panic(err)
			}
		}
		return l
	}
}

// boundHTTPAddr returns the control address this helper process bound.
func boundHTTPAddr() string {
	if p := childBoundAddr.Load(); p != nil {
		return *p
	}
	return ""
}

// publishedHTTPAddr reads the address a child published to path, if it has.
func publishedHTTPAddr(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// awaitChildControl waits, under awaitHA's bound, for the child to publish its
// bound address and answer GET /control/state with 200, then returns the address.
func awaitChildControl(t *testing.T, path string) string {
	t.Helper()
	var addr string
	awaitHA(t, func() bool {
		if addr == "" {
			a, ok := publishedHTTPAddr(path)
			if !ok {
				return false
			}
			addr = a
		}
		code, _ := haGet(t, "http://"+addr+"/control/state")
		return code == 200
	})
	return addr
}
