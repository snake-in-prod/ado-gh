package auth

import (
	"context"
	"time"

	"github.com/zalando/go-keyring"
)

// keyringTimeout bounds every keyring call: Secret Service can block forever
// on a locked collection.
var keyringTimeout = 60 * time.Second

// beforeKeyringCall is a test hook to simulate a hanging keyring.
var beforeKeyringCall = func() {}

// keyringUser is the account name; the service name carries host and org.
const keyringUser = ""

// TimeoutError is returned when a keyring call exceeds keyringTimeout.
type TimeoutError struct{ op string }

func (e *TimeoutError) Error() string { return "keyring " + e.op + " timed out" }

func withTimeout[T any](op string, f func() (T, error)) (T, error) {
	return withTimeoutCtx(context.Background(), op, f)
}

// withTimeoutCtx is withTimeout that also gives up when ctx is done.
func withTimeoutCtx[T any](ctx context.Context, op string, f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	hook := beforeKeyringCall
	go func() {
		hook()
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(keyringTimeout):
		var zero T
		return zero, &TimeoutError{op: op}
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func keyringSet(service, secret string) error {
	_, err := withTimeout("set", func() (struct{}, error) {
		return struct{}{}, keyring.Set(service, keyringUser, secret)
	})
	return err
}

func keyringGet(service string) (string, error) {
	return withTimeout("get", func() (string, error) { return keyring.Get(service, keyringUser) })
}

func keyringDelete(service string) error {
	_, err := withTimeout("delete", func() (struct{}, error) {
		return struct{}{}, keyring.Delete(service, keyringUser)
	})
	return err
}
