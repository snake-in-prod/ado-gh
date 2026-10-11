package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// throttleGet sends a GET through hc and returns the response status.
func throttleGet(ctx context.Context, t *testing.T, hc *http.Client) (int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://dev.azure.com/org/_apis/x", nil)
	require.NoError(t, err)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func throttled(status int, header, value string) httpmock.Responder {
	return httpmock.WithHeader(httpmock.StatusStringResponse(status, ""), header, value)
}

func TestTransportRetriesAfter429(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &httpmock.Registry{}
		reg.Register(httpmock.MatchAny, throttled(http.StatusTooManyRequests, "Retry-After", "7"))
		reg.Register(httpmock.MatchAny, httpmock.StringResponse("ok"))
		var log bytes.Buffer
		hc := NewHTTPClient(ClientOptions{Transport: reg, Log: &log})

		start := time.Now()
		status, err := throttleGet(context.Background(), t, hc)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, 7*time.Second, time.Since(start))
		assert.Len(t, reg.Requests, 2)
		assert.Contains(t, log.String(), "throttled; waiting 7s")
		reg.Verify(t)
	})
}

func TestTransport429WithoutRetryAfterWaitsDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &httpmock.Registry{}
		reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(http.StatusTooManyRequests, ""))
		reg.Register(httpmock.MatchAny, httpmock.StringResponse("ok"))
		hc := NewHTTPClient(ClientOptions{Transport: reg})

		start := time.Now()
		status, err := throttleGet(context.Background(), t, hc)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, time.Second, time.Since(start))
	})
}

func TestTransport429WithRetryAfterZeroRetriesNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &httpmock.Registry{}
		reg.Register(httpmock.MatchAny, throttled(http.StatusTooManyRequests, "Retry-After", "0"))
		reg.Register(httpmock.MatchAny, httpmock.StringResponse("ok"))
		hc := NewHTTPClient(ClientOptions{Transport: reg})

		start := time.Now()
		status, err := throttleGet(context.Background(), t, hc)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		assert.Zero(t, time.Since(start))
		reg.Verify(t)
	})
}

func TestTransportLogsDrainError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &httpmock.Registry{}
		reg.Register(httpmock.MatchAny, func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"1"}},
				Body: io.NopCloser(iotest.ErrReader(errors.New("reset"))), Request: req}, nil
		})
		reg.Register(httpmock.MatchAny, httpmock.StringResponse("ok"))
		var log bytes.Buffer
		hc := NewHTTPClient(ClientOptions{Transport: reg, Log: &log})

		status, err := throttleGet(context.Background(), t, hc)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, log.String(), "< drain error: reset")
	})
}

func TestTransportGivesUpAfterMaxRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &httpmock.Registry{}
		for range maxRetries + 1 {
			reg.Register(httpmock.MatchAny, throttled(http.StatusTooManyRequests, "Retry-After", "1"))
		}
		hc := NewHTTPClient(ClientOptions{Transport: reg})

		status, err := throttleGet(context.Background(), t, hc)
		require.NoError(t, err)
		assert.Equal(t, http.StatusTooManyRequests, status)
		assert.Len(t, reg.Requests, maxRetries+1)
	})
}

func TestTransportRetryCapsRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &httpmock.Registry{}
		reg.Register(httpmock.MatchAny, throttled(http.StatusTooManyRequests, "Retry-After", "3600"))
		reg.Register(httpmock.MatchAny, httpmock.StringResponse("ok"))
		hc := NewHTTPClient(ClientOptions{Transport: reg})

		start := time.Now()
		_, err := throttleGet(context.Background(), t, hc)
		require.NoError(t, err)
		assert.Equal(t, maxBackoff, time.Since(start))
	})
}

func TestTransportResendsBodyOnRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var bodies []string
		reg := &httpmock.Registry{}
		record := func(resp httpmock.Responder) httpmock.Responder {
			return func(req *http.Request) (*http.Response, error) {
				b, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				bodies = append(bodies, string(b))
				return resp(req)
			}
		}
		reg.Register(httpmock.MatchAny, record(throttled(http.StatusTooManyRequests, "Retry-After", "1")))
		reg.Register(httpmock.MatchAny, record(httpmock.StringResponse("ok")))
		hc := NewHTTPClient(ClientOptions{Transport: reg})

		req, err := http.NewRequest(http.MethodPost, "https://dev.azure.com/org/_apis/x", strings.NewReader(`{"a":1}`))
		require.NoError(t, err)
		resp, err := hc.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, []string{`{"a":1}`, `{"a":1}`}, bodies)
	})
}

func TestTransportSlowsDownOnSuccessfulThrottleHeaders(t *testing.T) {
	tests := []struct {
		name, header, value string
		wantWait            time.Duration
	}{
		{"Retry-After on 200", "Retry-After", "4", 4 * time.Second},
		{"X-RateLimit-Remaining 0", "X-RateLimit-Remaining", "0", time.Second},
		{"X-RateLimit-Remaining above 0", "X-RateLimit-Remaining", "12", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reg := &httpmock.Registry{}
				reg.Register(httpmock.MatchAny, throttled(http.StatusOK, tt.header, tt.value))
				reg.Register(httpmock.MatchAny, httpmock.StringResponse("ok"))
				hc := NewHTTPClient(ClientOptions{Transport: reg})

				start := time.Now()
				status, err := throttleGet(context.Background(), t, hc)
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, status)
				assert.Zero(t, time.Since(start), "the throttled 200 itself is returned at once")

				_, err = throttleGet(context.Background(), t, hc)
				require.NoError(t, err)
				assert.Equal(t, tt.wantWait, time.Since(start), "the next request waits")
			})
		})
	}
}

func TestTransportWaitHonoursContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &httpmock.Registry{}
		reg.Register(httpmock.MatchAny, throttled(http.StatusTooManyRequests, "Retry-After", "30"))
		hc := NewHTTPClient(ClientOptions{Transport: reg})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err := throttleGet(ctx, t, hc)
		assert.True(t, errors.Is(err, context.DeadlineExceeded), "err = %v", err)
		assert.Len(t, reg.Requests, 1)
	})
}
