// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"log"
	"net/http"
	"os"

	"github.com/rknightion/synthkit/e2e/receiver"
)

func main() {
	addr := ":9099"
	if v := os.Getenv("RECEIVER_ADDR"); v != "" {
		addr = v
	}
	certFile := os.Getenv("RECEIVER_TLS_CERT_FILE")
	keyFile := os.Getenv("RECEIVER_TLS_KEY_FILE")
	if certFile == "" || keyFile == "" {
		log.Fatal("RECEIVER_TLS_CERT_FILE and RECEIVER_TLS_KEY_FILE are required")
	}
	rec := receiver.New()
	log.Printf("e2e receiver listening with TLS on %s", addr)
	if err := http.ListenAndServeTLS(addr, certFile, keyFile, rec.Handler()); err != nil {
		log.Fatal(err)
	}
}
