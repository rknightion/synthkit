// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

type slowPanel struct {
	mu  sync.Mutex
	got []string
}

func (p *slowPanel) Panel(_ context.Context, s Snapshot) error {
	p.mu.Lock()
	p.got = append(p.got, s.State)
	p.mu.Unlock()
	return nil
}

// TestPanelFeedCoalesces offers a burst inside one interval: the panel sees the first and the
// newest only, never a duplicate, and final always lands.
func TestPanelFeedCoalesces(t *testing.T) {
	panelInterval = 200 * time.Millisecond
	defer func() { panelInterval = PanelInterval }()
	p := &slowPanel{}
	f := newPanelFeed(context.Background(), p, func(err error) { t.Error(err) })
	f.offer(Snapshot{State: "a"})
	time.Sleep(50 * time.Millisecond)
	f.offer(Snapshot{State: "b"})
	f.offer(Snapshot{State: "c"})
	f.offer(Snapshot{State: "c"})
	time.Sleep(400 * time.Millisecond)
	f.offer(Snapshot{State: "c"}) // unchanged since it was shown: not sent again
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.final(ctx, Snapshot{State: "end"}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if got := len(p.got); got != 3 || p.got[0] != "a" || p.got[1] != "c" || p.got[2] != "end" {
		t.Fatalf("panel saw %v, want [a c end]", p.got)
	}
}
