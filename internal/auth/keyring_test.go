package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithTimeoutCtx(t *testing.T) {
	t.Run("returns the call's result", func(t *testing.T) {
		v, err := withTimeoutCtx(context.Background(), "get", func() (string, error) { return "tok", nil })
		if v != "tok" || err != nil {
			t.Fatalf("got %q, %v", v, err)
		}
	})

	t.Run("times out", func(t *testing.T) {
		old := keyringTimeout
		keyringTimeout = 10 * time.Millisecond
		t.Cleanup(func() { keyringTimeout = old })
		block := make(chan struct{})
		t.Cleanup(func() { close(block) })
		_, err := withTimeoutCtx(context.Background(), "set", func() (string, error) { <-block; return "", nil })
		var te *TimeoutError
		if !errors.As(err, &te) || te.Error() != "keyring set timed out" {
			t.Fatalf("err = %v, want keyring set timeout", err)
		}
	})

	t.Run("stops when ctx is done", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		block := make(chan struct{})
		t.Cleanup(func() { close(block) })
		_, err := withTimeoutCtx(ctx, "delete", func() (string, error) { <-block; return "", nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}
