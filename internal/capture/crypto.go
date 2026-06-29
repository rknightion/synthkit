// SPDX-License-Identifier: AGPL-3.0-only

package capture

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
)

// Encrypt wraps plaintext in an age scrypt (passphrase) recipient. The passphrase is the key the
// customer hands us out-of-band.
func Encrypt(plaintext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("capture: empty passphrase")
	}
	rcp, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("capture: recipient: %w", err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rcp)
	if err != nil {
		return nil, fmt.Errorf("capture: encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("capture: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("capture: finalize: %w", err)
	}
	return buf.Bytes(), nil
}

// Decrypt reverses Encrypt with the same passphrase.
func Decrypt(ciphertext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("capture: empty passphrase")
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("capture: identity: %w", err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), id)
	if err != nil {
		return nil, fmt.Errorf("capture: decrypt (wrong passphrase or corrupt file?): %w", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("capture: read plaintext: %w", err)
	}
	return out, nil
}
