// SPDX-License-Identifier: AGPL-3.0-only

package fleet

// teardown.go — explicit owned teardown from the persisted ownership ledger.
//
// The selected-roster Unregister removes only what the current selection would register. The
// ledger can hold more: historical selections, scale-downs whose unregistration failed, or a
// paused kind. TeardownOwned inventories the ledger for the exact recorded destination and
// retires each recorded identity only after a confirmed-absence read. It never derives deletion
// authority from names, prefixes or the current roster.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
)

// TeardownOutcome is the remote outcome for one recorded collector identity.
type TeardownOutcome string

const (
	// OutcomeConfirmedAbsent: an exact not_found read observed the collector absent.
	OutcomeConfirmedAbsent TeardownOutcome = "confirmed_absent"
	// OutcomeRejected: Fleet Management definitely refused the deletion and the collector
	// was still read as present afterwards.
	OutcomeRejected TeardownOutcome = "rejected"
	// OutcomeUnknown: absence could not be confirmed (lost response, failed read, interruption).
	OutcomeUnknown TeardownOutcome = "unknown"
)

// TeardownStatus is the receipt's overall status. Only TeardownComplete is success.
type TeardownStatus string

const (
	TeardownComplete  TeardownStatus = "complete"
	TeardownRemaining TeardownStatus = "remaining"
	// TeardownNoLedger: no ledger document bound to this destination exists (for example a
	// missing file-backend ledger), so an empty inventory proves nothing.
	TeardownNoLedger TeardownStatus = "no_ledger"
)

// Reasons are a closed set so a receipt never carries upstream text.
const (
	ReasonAbsentBeforeDelete  = "absent_before_delete"
	ReasonAbsentAfterDelete   = "absent_after_delete"
	ReasonPresentAfterDelete  = "present_after_delete"
	ReasonDeleteRejected      = "delete_rejected"
	ReasonReadFailed          = "read_failed"
	ReasonLedgerReleaseFailed = "ledger_release_failed"
	ReasonNotAttempted        = "not_attempted"
	ReasonInterrupted         = "interrupted"
	ReasonAddedDuringTeardown = "added_during_teardown"
)

// TeardownEntry is one recorded identity's result. It carries the collector ID (a synthetic,
// non-secret identity), never a credential, destination, response body or upstream message.
type TeardownEntry struct {
	ID         string          `json:"id"`
	Outcome    TeardownOutcome `json:"outcome"`
	Reason     string          `json:"reason"`
	HTTPStatus int             `json:"http_status,omitempty"`
	// Retired is true only when the ledger entry was released after confirmed absence.
	Retired bool `json:"retired"`
}

// TeardownReceipt is the secret-free remaining-obligation receipt. Remaining counts ledger
// identities still held as cleanup intent after the run, including ones a concurrent writer
// added; InventoryVerified is false when the closing ledger re-read failed.
type TeardownReceipt struct {
	Status            TeardownStatus  `json:"status"`
	Inventoried       int             `json:"inventoried"`
	Retired           int             `json:"retired"`
	Remaining         int             `json:"remaining"`
	InventoryVerified bool            `json:"inventory_verified"`
	Collectors        []TeardownEntry `json:"collectors"`
}

// TeardownOptions bounds request pacing. MinInterval spaces every Fleet Management request.
type TeardownOptions struct {
	MinInterval time.Duration
}

// DefaultTeardownInterval keeps owned teardown under the documented default Collector API limit
// of 3 requests per second for non-collector endpoints (requests_per_second:api,
// https://grafana.com/docs/grafana-cloud/send-data/fleet-management/api-reference/collector-api/#rate-limits).
const DefaultTeardownInterval = 350 * time.Millisecond

var (
	// ErrTeardownIncomplete: the run ended with ledger cleanup intent still retained.
	ErrTeardownIncomplete = errors.New("Fleet owned teardown incomplete; remaining cleanup intent retained")
	// ErrTeardownNoLedger: no persisted ownership ledger is configured, so nothing can be certified.
	ErrTeardownNoLedger = errors.New("Fleet owned teardown requires a persisted ownership ledger")
	// ErrTeardownAuthority: the configured state cannot prove exclusive emitter authority.
	ErrTeardownAuthority = errors.New("Fleet owned teardown with Kubernetes state requires HA_MODE=lease")
	// ErrTeardownLedgerAbsent: the ledger document is absent or carries no destination binding.
	ErrTeardownLedgerAbsent = errors.New("Fleet owned teardown found no destination-bound ownership ledger; nothing can be certified")
)

// LedgerInventory is the ownership-ledger read owned teardown requires. bound is true only when
// a ledger document exists and is bound to this controller's destination, including an emptied
// one; an absent document or an unbound empty legacy document is not bound. Implementations
// still return the RegisteredStore refusals (destination mismatch, unbound nonempty intent).
type LedgerInventory interface {
	Inventory(context.Context) (ids []string, bound bool, err error)
}

// TeardownOwned permanently deletes every collector the persisted ledger records for this controller's
// exact destination, whatever the current selection. Ledger refusals (destination mismatch,
// nonempty unbound legacy intent, invalid or unreadable state) return before any request and with
// a nil receipt. Otherwise the receipt is always returned; the error is nil only when every
// inventoried identity was confirmed absent and released, and the closing re-read found the
// ledger empty.
func (c *Controller) TeardownOwned(ctx context.Context, opts TeardownOptions) (*TeardownReceipt, error) {
	if c.cfg.DryRun {
		return nil, operationalerr.New(operationalerr.CodeRejected)
	}
	if c.authMissing() {
		return nil, errors.New("fleet: registration credentials unavailable")
	}
	if c.cfg.Store == nil {
		return nil, ErrTeardownNoLedger
	}
	inventory, ok := c.cfg.Store.(LedgerInventory)
	if !ok {
		return nil, ErrTeardownNoLedger
	}
	// Owned teardown never follows a redirect to another host: its reads certify absence, and
	// HA-off clients otherwise keep net/http's default redirect policy.
	if c.client.http.CheckRedirect == nil {
		c.client.http.CheckRedirect = refuseCrossHostRedirect
	}
	var receipt *TeardownReceipt
	err := c.cfg.Bounded.Run(ctx, ha.Delivery, func(ctx context.Context) error {
		ids, bound, err := inventory.Inventory(ctx)
		if err != nil {
			return err
		}
		if !bound {
			receipt = &TeardownReceipt{Status: TeardownNoLedger, Collectors: []TeardownEntry{}}
			return ErrTeardownLedgerAbsent
		}
		ids = append([]string(nil), ids...)
		sort.Strings(ids)
		receipt = &TeardownReceipt{Inventoried: len(ids), Collectors: make([]TeardownEntry, 0, len(ids))}
		pace := newPacer(opts.MinInterval)
		for _, id := range ids {
			if ctx.Err() != nil {
				receipt.Collectors = append(receipt.Collectors, TeardownEntry{ID: id, Outcome: OutcomeUnknown, Reason: ReasonNotAttempted})
				continue
			}
			receipt.Collectors = append(receipt.Collectors, c.retire(ctx, id, pace))
		}
		c.closeReceipt(ctx, inventory, receipt, ids)
		return nil
	})
	if receipt == nil {
		return nil, err
	}
	if receipt.Status == TeardownNoLedger {
		return receipt, err
	}
	if receipt.InventoryVerified && receipt.Remaining == 0 && err == nil {
		receipt.Status = TeardownComplete
		return receipt, nil
	}
	receipt.Status = TeardownRemaining
	return receipt, errors.Join(ErrTeardownIncomplete, err)
}

// retire resolves one recorded identity: read, delete only if present, read again, and release
// ledger custody only on a confirmed-absence read. It uses the management DeleteCollector, never
// UnregisterCollector: an unregistered collector is only marked inactive and still reads present.
func (c *Controller) retire(ctx context.Context, id string, pace *pacer) TeardownEntry {
	entry := TeardownEntry{ID: id, Outcome: OutcomeUnknown, Reason: ReasonReadFailed}
	if !pace.wait(ctx) {
		entry.Reason = ReasonNotAttempted
		return entry
	}
	presence, status, _ := c.client.GetCollector(ctx, id)
	switch presence {
	case PresenceAbsent:
		return c.release(ctx, entry, ReasonAbsentBeforeDelete)
	case PresenceUnknown:
		entry.HTTPStatus = status
		return entry
	}
	if !pace.wait(ctx) {
		entry.Reason = ReasonNotAttempted
		return entry
	}
	deleteStatus, _ := c.client.deleteStatus(ctx, id)
	if !pace.wait(ctx) {
		entry.Reason, entry.HTTPStatus = ReasonInterrupted, deleteStatus
		return entry
	}
	presence, status, _ = c.client.GetCollector(ctx, id)
	switch presence {
	case PresenceAbsent:
		return c.release(ctx, entry, ReasonAbsentAfterDelete)
	case PresencePresent:
		if definiteRejection(deleteStatus) {
			entry.Outcome, entry.Reason, entry.HTTPStatus = OutcomeRejected, ReasonDeleteRejected, deleteStatus
			return entry
		}
		entry.Reason, entry.HTTPStatus = ReasonPresentAfterDelete, deleteStatus
		return entry
	default:
		entry.HTTPStatus = status
		return entry
	}
}

func (c *Controller) release(ctx context.Context, entry TeardownEntry, reason string) TeardownEntry {
	entry.Outcome, entry.Reason = OutcomeConfirmedAbsent, reason
	if err := c.persist(ctx, entry.ID, false); err != nil {
		entry.Reason = ReasonLedgerReleaseFailed
		return entry
	}
	entry.Retired = true
	delete(c.registered, entry.ID)
	delete(c.confirmed, entry.ID)
	return entry
}

// closeReceipt re-reads the ledger. Any identity still present, including one a concurrent
// writer added during the run, remains an obligation; completion is never inferred.
func (c *Controller) closeReceipt(ctx context.Context, inventory LedgerInventory, receipt *TeardownReceipt, inventoried []string) {
	retired := map[string]bool{}
	for _, e := range receipt.Collectors {
		if e.Retired {
			receipt.Retired++
			retired[e.ID] = true
		}
	}
	after, bound, err := inventory.Inventory(ctx)
	if err != nil || !bound {
		receipt.Remaining = receipt.Inventoried - receipt.Retired
		return
	}
	receipt.InventoryVerified = true
	known := make(map[string]bool, len(inventoried))
	for _, id := range inventoried {
		known[id] = true
	}
	sort.Strings(after)
	for _, id := range after {
		if !known[id] || retired[id] {
			receipt.Collectors = append(receipt.Collectors, TeardownEntry{ID: id, Outcome: OutcomeUnknown, Reason: ReasonAddedDuringTeardown})
		}
	}
	receipt.Remaining = len(after)
}

// definiteRejection is a non-ambiguous client error: the request reached Fleet Management and
// was refused. Timeouts, rate limits and 404 (which a proxy may also produce) stay unknown.
func definiteRejection(status int) bool {
	return status >= 400 && status < 500 && status != 404 && status != 408 && status != 429
}

type pacer struct {
	interval time.Duration
	last     time.Time
}

func newPacer(interval time.Duration) *pacer { return &pacer{interval: interval} }

// wait spaces requests by interval and reports false when ctx ends first.
func (p *pacer) wait(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if p.interval > 0 && !p.last.IsZero() {
		if d := p.interval - time.Since(p.last); d > 0 {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-t.C:
			}
		}
	}
	p.last = time.Now()
	return true
}

// refuseCrossHostRedirect stops at a redirect to a different scheme or host, so the 3xx is
// reported as an unknown outcome instead of being answered by another server.
func refuseCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host || len(via) >= 10 {
		return http.ErrUseLastResponse
	}
	return nil
}
