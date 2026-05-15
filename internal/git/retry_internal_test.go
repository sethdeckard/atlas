package git

import (
	"context"
	"errors"
	"testing"
)

// TestShouldRetryGit_NoRetryOnTimeout guards against a regression
// where runGit retried any signal-killed exec.ExitError. Since
// CommandContext kills hung children by signal too, that meant a
// per-call timeout would burn ~10s of wall time across two attempts
// instead of the intended single 5s budget. The retry must skip
// when attemptGit reports its deadline fired.
func TestShouldRetryGit_NoRetryOnTimeout(t *testing.T) {
	// Even a "transient signal"-looking error doesn't get retried
	// when timedOut is true. Use a sentinel error so the test
	// doesn't depend on isTransientSignal — the gate stops earlier.
	err := errors.New("signal: killed")
	if shouldRetryGit(context.Background(), err, true) {
		t.Errorf("shouldRetryGit returned true for a timed-out attempt; expected false")
	}
}

// TestShouldRetryGit_NoRetryOnCancel covers the parent-cancelled
// path: the caller has given up, so even a transient signal-kill
// shouldn't trigger another 5s attempt.
func TestShouldRetryGit_NoRetryOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := errors.New("signal: segmentation fault")
	if shouldRetryGit(ctx, err, false) {
		t.Errorf("shouldRetryGit returned true for a cancelled parent ctx; expected false")
	}
}

// TestShouldRetryGit_NoRetryOnNilError confirms the success short-
// circuit — no retry when there's nothing to retry.
func TestShouldRetryGit_NoRetryOnNilError(t *testing.T) {
	if shouldRetryGit(context.Background(), nil, false) {
		t.Errorf("shouldRetryGit returned true for a nil error; expected false")
	}
}
