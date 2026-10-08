// SPDX-License-Identifier: AGPL-3.0-only
package ha

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPreparationCapabilityAndTerminalInterlocks(t *testing.T) {
	for _, terminate := range []string{"quiesce", "revoke", "cancel", "none"} {
		t.Run(terminate, func(t *testing.T) {
			g := NewGate()
			lead, cancel := context.WithCancel(context.Background())
			defer cancel()
			var leaked context.Context
			err := g.Activate(lead, func(ctx context.Context) error {
				leaked = ctx
				if g.Role() != RoleStandby {
					t.Fatal("preparation published leader")
				}
				for _, tc := range []struct {
					ctx     context.Context
					op      Operation
					allowed bool
				}{{ctx, Mutation, true}, {ctx, Delivery, false}, {lead, Mutation, false}, {lead, Delivery, false}} {
					called := false
					e := g.Do(tc.ctx, tc.op, func(context.Context) error { called = true; return nil })
					if called != tc.allowed || (!tc.allowed && !errors.Is(e, ErrNotLeader)) {
						t.Fatalf("admission op=%v allowed=%v called=%v err=%v", tc.op, tc.allowed, called, e)
					}
				}
				switch terminate {
				case "quiesce":
					g.Quiesce()
				case "revoke":
					g.Revoke()
				case "cancel":
					cancel()
				}
				return nil
			})
			if (err == nil) != (terminate == "none") {
				t.Fatalf("activation %v", err)
			}
			if !errors.Is(g.Do(leaked, Mutation, func(context.Context) error { t.Fatal("leaked capability accepted"); return nil }), ErrNotLeader) {
				t.Fatal("leaked token did not fail closed")
			}
			if !errors.Is(g.Activate(lead, func(context.Context) error { t.Fatal("second preparation"); return nil }), ErrNotLeader) {
				t.Fatal("reacquisition accepted")
			}
			if terminate == "none" {
				g.Quiesce()
				if g.Role() != RoleLeader {
					t.Fatal("active drain lost role")
				}
				if !errors.Is(g.Do(lead, Mutation, func(context.Context) error { t.Fatal("quiesced mutation"); return nil }), ErrNotLeader) {
					t.Fatal("mutation not closed")
				}
				if err := g.Do(lead, Delivery, func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
			}
			g.Revoke()
			if g.Role() != RoleStandby {
				t.Fatal("revoke retained role")
			}
		})
	}
}

func TestRevokeCancelsAndWaitJoins(t *testing.T) {
	g := NewGate()
	if err := g.Activate(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- g.Do(context.Background(), Delivery, func(ctx context.Context) error { close(started); <-ctx.Done(); <-finish; return ctx.Err() })
	}()
	<-started
	g.Revoke()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if !errors.Is(g.Wait(ctx), context.DeadlineExceeded) {
		t.Fatal("wait claimed abandoned function completed")
	}
	close(finish)
	<-done
	if err := g.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(g.Do(context.Background(), Delivery, func(context.Context) error { t.Fatal("post-fence attempt"); return nil }), ErrNotLeader) {
		t.Fatal("fence")
	}
}

func TestConcurrentActivationTermination(t *testing.T) {
	for i := 0; i < 100; i++ {
		g := NewGate()
		entered := make(chan struct{})
		finish := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Activate(context.Background(), func(context.Context) error { close(entered); <-finish; return nil })
		}()
		<-entered
		g.Quiesce()
		close(finish)
		wg.Wait()
		if g.Role() != RoleStandby {
			t.Fatal("activation reopened after termination")
		}
	}
}

func TestPreparationCannotPublishWithLiveChild(t *testing.T) {
	g := NewGate()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	err := g.Activate(context.Background(), func(ctx context.Context) error {
		go func() {
			done <- g.Do(ctx, Mutation, func(context.Context) error { close(entered); <-release; return nil })
		}()
		<-entered
		return nil
	})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatal("unjoined preparation accepted", err)
	}
	close(release)
	<-done
}
