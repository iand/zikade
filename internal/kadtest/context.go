package kadtest

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// CtxShort returns a Context for tests that are expected to complete quickly.
// The context will be cancelled after 10 seconds or just before the test
// binary deadline (as specified by the -timeout flag when running the test), whichever
// is sooner.
func CtxShort(t *testing.T) context.Context {
	t.Helper()

	var timeout time.Duration
	// Increase the timeout for 32-bit Windows
	if runtime.GOOS == "windows" && runtime.GOARCH == "386" {
		timeout = 60 * time.Second
	} else {
		timeout = 10 * time.Second
	}
	goal := time.Now().Add(timeout)

	deadline, ok := t.Deadline()
	if !ok {
		deadline = goal
	} else {
		deadline = deadline.Add(-time.Second)
		if deadline.After(goal) {
			deadline = goal
		}
	}

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	t.Cleanup(cancel)
	return ctx
}

// CtxBubble returns a Context for tests running inside a [testing/synctest]
// bubble. The context is cancelled after 10 seconds of the bubble's fake time,
// which passes no real time but bounds any wait that would otherwise deadlock
// the bubble.
//
// [CtxShort] cannot be used inside a bubble because it calls T.Deadline, which
// synctest forbids.
func CtxBubble(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
