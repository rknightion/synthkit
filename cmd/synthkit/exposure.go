// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type controlExposure struct {
	HTTPAddr string
	HostBind string
	Token    string
	Ack      string
}

func validateControlExposure(exposure controlExposure, container, servesHTTP bool) error {
	if exposure.Ack != "" && exposure.Ack != "trusted-network" && exposure.Ack != "tls-proxy" {
		return fmt.Errorf("CONTROL_EXPOSURE_ACK must be exactly trusted-network or tls-proxy")
	}
	if !servesHTTP {
		return nil
	}

	var (
		loopback bool
		bindName string
	)
	if container {
		bindName = "SYNTHKIT_BIND"
		if exposure.HostBind == "" {
			return fmt.Errorf("SYNTHKIT_BIND is required inside a container so host exposure can be validated")
		}
		var err error
		loopback, err = isLoopbackHost(exposure.HostBind)
		if err != nil {
			return fmt.Errorf("invalid SYNTHKIT_BIND %q: %w", exposure.HostBind, err)
		}
	} else {
		bindName = "JSON_HTTP_ADDR"
		host, port, err := net.SplitHostPort(exposure.HTTPAddr)
		if err != nil {
			return fmt.Errorf("invalid JSON_HTTP_ADDR %q: %w", exposure.HTTPAddr, err)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid JSON_HTTP_ADDR %q: port must be between 1 and 65535", exposure.HTTPAddr)
		}
		loopback, err = isLoopbackHost(host)
		if err != nil {
			return fmt.Errorf("invalid JSON_HTTP_ADDR %q: %w", exposure.HTTPAddr, err)
		}
	}

	if loopback {
		return nil
	}
	if exposure.Token == "" {
		return fmt.Errorf("unsafe non-loopback %s requires CONTROL_TOKEN", bindName)
	}
	if exposure.Ack == "" {
		return fmt.Errorf("unsafe non-loopback %s requires CONTROL_EXPOSURE_ACK=trusted-network or tls-proxy", bindName)
	}
	return nil
}

func isLoopbackHost(host string) (bool, error) {
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "" {
		return false, nil
	}
	if host == "localhost" {
		return true, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback(), nil
	}
	if strings.ContainsAny(host, " /\\\t\r\n") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false, fmt.Errorf("host is not a valid IP address or hostname")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false, fmt.Errorf("host is not a valid IP address or hostname")
		}
	}
	return false, nil
}
