package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const reviewMeID = "33333333-3333-3333-3333-333333333333"

type reviewEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// bodies holds the decoded JSON body of the vote and thread requests, keyed "vote"/"thread".
	bodies map[string]map[string]any
}

// newReviewEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newReviewEnv(t *testing.T, tty bool) *reviewEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	// One byte per Read keeps each prompt's line scanner from swallowing the next answers.
	ios.In = iotest.OneByteReader(in)
	org := "prreview-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	return &reviewEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, bodies: map[string]map[string]any{}}
}

func (e *reviewEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdReview(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// capture responds with resp after storing the JSON request body under key.
func (e *reviewEnv) capture(key string, resp any) httpmock.Responder {
	return func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		e.bodies[key] = m
		return httpmock.JSONResponse(resp)(req)
	}
}

// stubPR stubs the Git locations and the lookup of pull request 7.
func (e *reviewEnv) stubPR() {
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/7"),
		httpmock.JSONResponse(map[string]any{"pullRequestId": 7, "title": "Fix", "status": "active", "repository": map[string]any{"name": "web"}}))
}

// stubVote stubs connectionData (the caller's id) and the vote PUT.
func (e *reviewEnv) stubVote() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": reviewMeID}}))
	e.reg.Register(httpmock.REST(http.MethodPut, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/7/reviewers/"+reviewMeID),
		e.capture("vote", map[string]any{"id": reviewMeID}))
}

func (e *reviewEnv) stubThread() {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/7/threads"),
		e.capture("thread", map[string]any{"id": 1}))
}

func threadContent(t *testing.T, body map[string]any) string {
	t.Helper()
	comments, _ := body["comments"].([]any)
	require.Len(t, comments, 1)
	return comments[0].(map[string]any)["content"].(string)
}

func TestReviewVotes(t *testing.T) {
	tests := []struct {
		flag    string
		vote    float64
		message string
	}{
		{"--approve", 10, "Approved pull request !7"},
		{"-a", 10, "Approved pull request !7"},
		{"--approve-with-suggestions", 5, "Approved pull request !7 with suggestions"},
		{"--wait", -5, "Voted wait for author on pull request !7"},
		{"--request-changes", -5, "Voted wait for author on pull request !7"},
		{"-r", -5, "Voted wait for author on pull request !7"},
		{"--reject", -10, "Rejected pull request !7"},
		{"--reset", 0, "Reset your vote on pull request !7"},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			e := newReviewEnv(t, true)
			e.stubPR()
			e.stubVote()

			require.NoError(t, e.run(t, "7", tt.flag))
			e.reg.Verify(t)
			assert.Equal(t, tt.vote, e.bodies["vote"]["vote"])
			assert.Equal(t, reviewMeID, e.bodies["vote"]["id"])
			assert.Equal(t, "✓ "+tt.message+"\n", e.errOut.String())
		})
	}
}

func TestReviewNonTTYIsQuiet(t *testing.T) {
	e := newReviewEnv(t, false)
	e.stubPR()
	e.stubVote()

	require.NoError(t, e.run(t, "7", "--approve"))
	assert.Empty(t, e.errOut.String())
	assert.Empty(t, e.out.String())
}

func TestReviewCommentCreatesThread(t *testing.T) {
	e := newReviewEnv(t, false)
	e.stubPR()
	e.stubThread()

	require.NoError(t, e.run(t, "7", "--comment", "-b", "Looks odd"))
	e.reg.Verify(t)
	assert.Equal(t, "Looks odd", threadContent(t, e.bodies["thread"]))
	assert.Equal(t, "active", e.bodies["thread"]["status"])
	assert.NotContains(t, e.bodies, "vote")
}

func TestReviewVoteWithBodyAddsThread(t *testing.T) {
	e := newReviewEnv(t, false)
	e.stubPR()
	e.stubVote()
	e.stubThread()

	require.NoError(t, e.run(t, "7", "--approve", "--body", "LGTM"))
	e.reg.Verify(t)
	assert.Equal(t, float64(10), e.bodies["vote"]["vote"])
	assert.Equal(t, "LGTM", threadContent(t, e.bodies["thread"]))
}

func TestReviewFlagErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"two votes", []string{"--approve", "--reject"}, "only one of"},
		{"wait and request changes", []string{"--wait", "-r"}, "only one of"},
		{"vote and comment", []string{"--wait", "--comment", "-b", "x"}, "only one of"},
		{"comment without body", []string{"--comment"}, "--body is required with --comment"},
		{"blank comment body", []string{"--comment", "-b", "  "}, "--body is required with --comment"},
		{"no flag non-TTY", nil, "--approve, --approve-with-suggestions, --wait, --reject, --reset or --comment required when not running interactively"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newReviewEnv(t, false)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			var fe *cmdutil.FlagError
			assert.True(t, errors.As(err, &fe), "want FlagError, got %T", err)
			assert.Empty(t, e.reg.Requests)
		})
	}
}

func TestReviewInteractive(t *testing.T) {
	t.Run("vote with body", func(t *testing.T) {
		e := newReviewEnv(t, true)
		e.stubPR()
		e.stubVote()
		e.stubThread()
		// Option 2 = Approve with suggestions, then the optional comment.
		e.in.WriteString("2\nNice work\n")

		require.NoError(t, e.run(t, "7"))
		e.reg.Verify(t)
		assert.Equal(t, float64(5), e.bodies["vote"]["vote"])
		assert.Equal(t, "Nice work", threadContent(t, e.bodies["thread"]))
	})
	t.Run("vote without body", func(t *testing.T) {
		e := newReviewEnv(t, true)
		e.stubPR()
		e.stubVote()
		e.in.WriteString("4\n\n")

		require.NoError(t, e.run(t, "7"))
		e.reg.Verify(t)
		assert.Equal(t, float64(-10), e.bodies["vote"]["vote"])
		assert.NotContains(t, e.bodies, "thread")
	})
	t.Run("comment", func(t *testing.T) {
		e := newReviewEnv(t, true)
		e.stubPR()
		e.stubThread()
		e.in.WriteString("6\nPlease explain\n")

		require.NoError(t, e.run(t, "7"))
		e.reg.Verify(t)
		assert.Equal(t, "Please explain", threadContent(t, e.bodies["thread"]))
		assert.Equal(t, "✓ Commented on pull request !7\n", e.errOut.String())
	})
	t.Run("comment with blank body", func(t *testing.T) {
		e := newReviewEnv(t, true)
		e.stubPR()
		e.in.WriteString("6\n\n")

		err := e.run(t, "7")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "comment cannot be blank")
	})
}

func TestReviewErrors(t *testing.T) {
	t.Run("vote fails", func(t *testing.T) {
		e := newReviewEnv(t, false)
		e.stubPR()
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
			httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": reviewMeID}}))
		e.reg.Register(httpmock.REST(http.MethodPut, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/7/reviewers/"+reviewMeID),
			httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
		err := e.run(t, "7", "--approve")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "denied")
	})
	t.Run("pr not found", func(t *testing.T) {
		e := newReviewEnv(t, false)
		e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/7"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "nope"}))
		err := e.run(t, "7", "--approve")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pull request !7 not found")
	})
	t.Run("current user fails", func(t *testing.T) {
		e := newReviewEnv(t, false)
		e.stubPR()
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
		err := e.run(t, "7", "--reset")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading the current user")
	})
	t.Run("thread fails", func(t *testing.T) {
		e := newReviewEnv(t, false)
		e.stubPR()
		e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/7/threads"),
			httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "bad thread"}))
		err := e.run(t, "7", "-c", "-b", "hi")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad thread")
	})
}
