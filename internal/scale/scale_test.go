// SPDX-License-Identifier: AGPL-3.0-only

package scale

import (
	"sync"
	"testing"
)

func TestCountDefaultAndOverride(t *testing.T) {
	s := New()
	if got := s.Count("api", 4); got != 4 {
		t.Errorf("unset → default: got %d want 4", got)
	}
	s.Set(map[string]int{"api": 12})
	if got := s.Count("api", 4); got != 12 {
		t.Errorf("override: got %d want 12", got)
	}
	if got := s.Count("other", 2); got != 2 {
		t.Errorf("unset key → default: got %d want 2", got)
	}
}

func TestSetIsAtomicallyVisible(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Count("api", 1) }()
	}
	s.Set(map[string]int{"api": 9})
	wg.Wait()
	if got := s.Count("api", 1); got != 9 {
		t.Errorf("got %d want 9", got)
	}
}
