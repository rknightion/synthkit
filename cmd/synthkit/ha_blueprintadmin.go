// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"fmt"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/control"
)

// haBlueprintAdmin retains the legacy read-only views while threading the request's
// absolute operation deadline through all source mutations (never Background).
type haBlueprintAdmin struct{ *blueprintAdminAdapter }

func (a *haBlueprintAdmin) UpsertSourceContext(ctx context.Context, sv control.SourceView) error {
	return a.mgr.UpsertSourceContext(ctx, bpsource.Source{ID: sv.ID, Name: sv.Name, Namespace: sv.Namespace, URL: sv.URL, Ref: sv.Ref, Subpath: sv.Subpath, TokenEnvVar: sv.TokenEnvVar})
}
func (a *haBlueprintAdmin) RemoveSourceContext(ctx context.Context, id string) error {
	contextual, ok := a.sc.(interface {
		RemoveSourceContext(context.Context, string) error
	})
	if !ok {
		return fmt.Errorf("HA source persistence lacks context admission")
	}
	return contextual.RemoveSourceContext(ctx, id)
}
func (a *haBlueprintAdmin) FetchNowContext(ctx context.Context, id string) error {
	return a.mgr.FetchNow(ctx, id)
}
