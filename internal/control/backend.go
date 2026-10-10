// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
)

type Key string

const (
	Control               Key = "control"
	BootManifest          Key = "boot-manifest"
	MaxStateDocumentBytes     = 786432
)

type Revision string
type Snapshot struct {
	Data     []byte
	Revision Revision
}

var ErrConflict = errors.New("state revision conflict")
var ErrOutcomeUnknown = errors.New("state outcome unknown")
var stateID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func GitSourceKey(id string) (Key, error) {
	if !stateID.MatchString(id) || strings.Contains(id, "__") {
		return "", fmt.Errorf("state: invalid source ID")
	}
	return Key("git-source/" + id), nil
}

type StateBackend interface {
	Load(context.Context, Key) (Snapshot, error)
	CompareAndSwap(context.Context, Key, Revision, []byte) (Revision, error)
}
type FileBackendOptions struct {
	Gate  ha.LeaderGate
	Paths map[Key]string
}
type KubernetesBackendOptions struct {
	Gate             ha.LeaderGate
	Namespace        string
	Objects          map[Key]string
	RequestTimeout   time.Duration
	MaxDocumentBytes int
}

func validKey(key Key) bool {
	if key == Control || key == BootManifest {
		return true
	}
	s := string(key)
	if len(s) > 11 && s[:11] == "git-source/" {
		_, err := GitSourceKey(s[11:])
		return err == nil
	}
	return false
}
