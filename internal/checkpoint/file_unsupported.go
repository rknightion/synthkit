// SPDX-License-Identifier: AGPL-3.0-only

//go:build !linux && !darwin

package checkpoint

import (
	"context"
	"os"
)

func checkpointPlatformSupported() bool                             { return false }
func checkpointFileIdentity(os.FileInfo) ([2]uint64, bool)          { return [2]uint64{}, false }
func openCheckpointFile(string, int, os.FileMode) (*os.File, error) { return nil, ErrUnsupported }
func lockCheckpointFile(context.Context, string) (*os.File, error)  { return nil, ErrUnsupported }
