package api

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tokenHeader = "X-MS-ContinuationToken"

// pages registers one stub per body; a non-empty token is sent as the continuation header.
func pages(bodies []string, tokens ...string) func(*httpmock.Registry) {
	return func(reg *httpmock.Registry) {
		for i, b := range bodies {
			resp := httpmock.StringResponse(b)
			if i < len(tokens) && tokens[i] != "" {
				resp = httpmock.WithHeader(resp, tokenHeader, tokens[i])
			}
			reg.Register(httpmock.REST("GET", "contoso/"+projects), resp)
		}
	}
}

func TestPaginateGETOnly(t *testing.T) {
	for _, args := range [][]string{{"-X", "POST"}, {"-f", "a=b"}, {"--input", "-"}} {
		r := run(t, setup{}, nil, append([]string{projects, "--paginate"}, args...)...)
		var flagErr *cmdutil.FlagError
		assert.ErrorAs(t, r.err, &flagErr, args)
		assert.Empty(t, r.reqs)
	}
}

func TestPaginateHeaderToken(t *testing.T) {
	r := run(t, setup{}, pages([]string{
		`{"count":2,"value":[1,2]}`,
		`{"count":1,"value":[3]}`,
	}, "tok 1"), projects+"?stateFilter=all", "--paginate")
	require.NoError(t, r.err)
	require.Len(t, r.reqs, 2)
	q := r.reqs[1].URL.Query()
	assert.Equal(t, "tok 1", q.Get("continuationToken"))
	assert.Equal(t, "7.1", q.Get("api-version"))
	assert.Equal(t, "all", q.Get("stateFilter"))
	assert.False(t, r.reqs[0].URL.Query().Has("continuationToken"))
	assert.Equal(t, "{\"count\":3,\"value\":[1,2,3]}\n", r.out, "value arrays merged, count recomputed")
}

func TestPaginateRepeatedToken(t *testing.T) {
	r := run(t, setup{}, pages([]string{`{"value":[1]}`, `{"value":[2]}`}, "a", "a"), projects, "--paginate")
	require.NoError(t, r.err)
	assert.Len(t, r.reqs, 2)
	assert.Equal(t, "{\"count\":2,\"value\":[1,2]}\n", r.out)
}

func TestPaginateBodyToken(t *testing.T) {
	tests := []struct {
		name   string
		bodies []string
		want   string
	}{
		{
			name: "isLastBatch stops",
			bodies: []string{
				`{"values":[1],"continuationToken":"a","isLastBatch":false}`,
				`{"values":[2],"continuationToken":"b","isLastBatch":true}`,
			},
			want: `{"values":[1],"continuationToken":"a","isLastBatch":false}{"values":[2],"continuationToken":"b","isLastBatch":true}`,
		},
		{
			name: "hasMore false stops",
			bodies: []string{
				`{"value":[1],"continuationToken":"a"}`,
				`{"value":[2],"continuationToken":"b","hasMore":false}`,
			},
			want: "{\"count\":2,\"value\":[1,2]}\n", // the stale token is dropped
		},
		{
			name:   "no token stops",
			bodies: []string{`{"values":[1],"continuationToken":""}`},
			want:   `{"values":[1],"continuationToken":""}`,
		},
		{
			name:   "non-object page",
			bodies: []string{`[1,2]`},
			want:   `[1,2]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := run(t, setup{}, pages(tt.bodies), projects, "--paginate")
			require.NoError(t, r.err)
			require.Len(t, r.reqs, len(tt.bodies))
			if len(r.reqs) > 1 {
				assert.Equal(t, "a", r.reqs[1].URL.Query().Get("continuationToken"))
			}
			assert.Equal(t, tt.want, r.out)
		})
	}
}

func TestPaginateTopSkip(t *testing.T) {
	r := run(t, setup{}, pages([]string{
		`{"count":2,"value":[1,2]}`,
		`{"count":1,"value":[3]}`,
		`{"count":0,"value":[]}`,
	}), projects+"?$top=2&$skip=10", "--paginate")
	require.NoError(t, r.err)
	require.Len(t, r.reqs, 3)
	var skips []string
	for _, req := range r.reqs {
		assert.Equal(t, "2", req.URL.Query().Get("$top"))
		skips = append(skips, req.URL.Query().Get("$skip"))
	}
	assert.Equal(t, []string{"10", "12", "13"}, skips, "$skip steps by the items received")
	assert.Equal(t, "{\"count\":3,\"value\":[1,2,3]}\n", r.out)

	// Without $top there is no stepping; a page without value or an unchanged page stops too.
	r = run(t, setup{}, pages([]string{`{"value":[1]}`}), projects, "--paginate")
	require.NoError(t, r.err)
	assert.Len(t, r.reqs, 1)

	r = run(t, setup{}, pages([]string{`{"x":1}`}), projects+"?$top=1", "--paginate")
	require.NoError(t, r.err)
	assert.Len(t, r.reqs, 1)

	r = run(t, setup{}, pages([]string{`{"value":[1]}`, `{"value":[1]}`}), projects+"?$top=1", "--paginate")
	require.NoError(t, r.err)
	assert.Len(t, r.reqs, 2, "a server ignoring $skip does not loop forever")
}

func TestPaginateSequentialWithInclude(t *testing.T) {
	r := run(t, setup{}, pages([]string{`{"value":[1]}`, `{"other":2}`}, "a"), projects, "--paginate", "-i")
	require.NoError(t, r.err)
	assert.Equal(t, "HTTP/1.1 200 OK\nX-Ms-Continuationtoken: a\n\n{\"value\":[1]}"+
		"HTTP/1.1 200 OK\n\n{\"other\":2}", r.out)

	r = run(t, setup{}, pages([]string{`{"value":[1]}`, `{"value":[2]}`}, "a"), projects, "--paginate", "-i")
	require.NoError(t, r.err)
	assert.Equal(t, "HTTP/1.1 200 OK\nX-Ms-Continuationtoken: a\n\nHTTP/1.1 200 OK\n\n{\"count\":2,\"value\":[1,2]}\n", r.out,
		"headers per page, then the merged body")
}

func TestPaginateJQOnMergedResult(t *testing.T) {
	bodies := []string{`{"value":[{"n":"a"}]}`, `{"value":[{"n":"b"}]}`}
	r := run(t, setup{}, pages(bodies, "x"), projects, "--paginate", "--jq", ".count")
	require.NoError(t, r.err)
	assert.Equal(t, "2\n", r.out)

	r = run(t, setup{}, pages(bodies, "x"), projects, "--paginate", "-t", "{{range .value}}{{.n}}{{end}}")
	require.NoError(t, r.err)
	assert.Equal(t, "ab", r.out)
}

func TestPaginateErrorPage(t *testing.T) {
	r := run(t, setup{}, func(reg *httpmock.Registry) {
		reg.Register(httpmock.MatchAny, httpmock.WithHeader(httpmock.StringResponse(`{"value":[1]}`), tokenHeader, "a"))
		reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(500, "boom"))
	}, projects, "--paginate")
	assert.ErrorContains(t, r.err, "HTTP 500")
	assert.Equal(t, "boom\n", r.errOut)
	assert.Empty(t, r.out)
}
