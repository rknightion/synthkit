// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"errors"
	"time"
)

// oneShotContext rejects expired entry before configuration loading. Its context
// bounds outbound preflight requests; offline validation checks it again before
// reporting success. It is not a receiver-side transaction or a kernel deadline.
func oneShotContext(raw string, present, allowed bool) (context.Context, context.CancelFunc, error) {
	if !present {
		return context.Background(), func() {}, nil
	}
	if !allowed {
		return nil, nil, errors.New("one-shot deadline requires exactly one validation or preflight mode")
	}
	deadline, e := time.Parse(time.RFC3339Nano, raw)
	if e != nil {
		return nil, nil, errors.New("invalid one-shot deadline; RFC3339 time required")
	}
	if !time.Now().Before(deadline) {
		return nil, nil, errors.New("one-shot deadline has expired")
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	return ctx, cancel, nil
}
