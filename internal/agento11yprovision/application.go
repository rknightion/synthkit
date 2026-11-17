// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"bytes"
	"context"
)

// PreparedPlan owns the existing engine's immutable typed plan. It conveys no
// authorization, admission, receipt, or permission to choose a transport route.
type PreparedPlan struct{ engine enginePlan }

func Prepare(in PlanInput) (PreparedPlan, error) {
	p, e := prepareEngine(in)
	return PreparedPlan{engine: p}, e
}
func (p PreparedPlan) Digest() Digest { return p.engine.plan.Digest() }
func PreviewPrepared(ctx context.Context, c *Client, s *LockedState, p PreparedPlan, mode Mode) (Preview, error) {
	return previewEngine(ctx, c, s, p.engine, mode)
}
func ApplyPrepared(ctx context.Context, c *Client, s *LockedState, p PreparedPlan, v Preview, a Approval) (Receipt, error) {
	return applyEngine(ctx, c, s, p.engine, v, a)
}
func RecoverPrepared(ctx context.Context, c *Client, s *LockedState, p PreparedPlan, v Preview, a Approval) (Receipt, error) {
	return recoverEngine(ctx, c, s, p.engine, v, a)
}
func CleanupPrepared(ctx context.Context, c *Client, s *LockedState, p PreparedPlan, v Preview, a Approval) (Receipt, error) {
	return cleanupEngine(ctx, c, s, p.engine, v, a)
}

// DecodeBinding reads the existing private custody artifact, not a public
// destination configuration. Both exact engine revisions are mandatory.
func DecodeBinding(raw []byte) (Binding, error) {
	var wire custodyBindingWire
	if e := custodyDecode(raw, &wire, custodySpecBytes); e != nil {
		return Binding{}, e
	}
	b := bindingFromWire(wire)
	if !custodyBinding(b) || b.ContractRevision != engineContractRevision || b.SourceRevision != engineSourceRevision {
		return Binding{}, ErrBinding
	}
	return b, nil
}

// DecodePreparedPreview rehydrates existing wire evidence offline. Equality
// here establishes neither freshness nor authorization: C must reobserve it.
func DecodePreparedPreview(p PreparedPlan, raw []byte) (Preview, error) {
	var w previewWire
	if e := custodyDecode(raw, &w, custodyMaxBytes); e != nil {
		return Preview{}, e
	}
	if w.Schema != 1 || w.Input != p.Digest() || bindingFromWire(w.Binding) != p.engine.plan.input.Binding {
		return Preview{}, ErrConflict
	}
	v, e := NewPreview(p.engine.plan, w.Mode, baseFromWire(w.Base), w.Inventory)
	if e != nil {
		return Preview{}, e
	}
	if !bytes.Equal(raw, v.JSON()) {
		return Preview{}, ErrConflict
	}
	return v, nil
}
