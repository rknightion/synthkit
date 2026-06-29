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
	rec := receiver.New()
	log.Printf("e2e receiver listening on %s", addr)
	if err := http.ListenAndServe(addr, rec.Handler()); err != nil {
		log.Fatal(err)
	}
}
