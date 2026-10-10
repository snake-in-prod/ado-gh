package httpmock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeT struct{ errs []string }

func (f *fakeT) Helper() {}
func (f *fakeT) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

type result struct {
	StatusCode int
	Header     http.Header
	Request    *http.Request
}

// do sends a request and returns the response metadata and its body (read and closed).
func do(t *testing.T, c *http.Client, method, url string) (result, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{resp.StatusCode, resp.Header, resp.Request}, string(b)
}

func TestRegistry_MatchesAndVerifies(t *testing.T) {
	c := &http.Client{}
	reg := ReplaceTripper(c)
	reg.Register(REST("GET", "org/_apis/projects"), StringResponse("one"))
	reg.Register(REST("GET", "org/_apis/projects"), StringResponse("two"))
	reg.Register(REST("POST", "org/_apis/other"), StringResponse("unused"))

	// Each stub matches once, in registration order; method is case-insensitive, query ignored.
	if _, got := do(t, c, "get", "https://dev.azure.com/org/_apis/projects?api-version=7.1"); got != "one" {
		t.Errorf("first call = %q", got)
	}
	if _, got := do(t, c, "GET", "https://dev.azure.com/org/_apis/projects"); got != "two" {
		t.Errorf("second call = %q", got)
	}
	if len(reg.Requests) != 2 {
		t.Errorf("Requests = %d, want 2", len(reg.Requests))
	}

	// Exhausted stubs and unknown paths fail.
	resp, err := c.Get("https://dev.azure.com/org/_apis/projects")
	if err == nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "no registered HTTP stubs matched") {
		t.Errorf("unmatched err = %v", err)
	}

	ft := &fakeT{}
	reg.Verify(ft)
	if len(ft.errs) != 1 || !strings.Contains(ft.errs[0], "1 HTTP stubs unmatched") {
		t.Errorf("Verify errs = %v", ft.errs)
	}

	all := &http.Client{}
	reg2 := ReplaceTripper(all)
	reg2.Register(MatchAny, StringResponse("x"))
	do(t, all, "DELETE", "https://example.com/a")
	ft2 := &fakeT{}
	reg2.Verify(ft2)
	if len(ft2.errs) != 0 {
		t.Errorf("Verify errs = %v", ft2.errs)
	}
}

func TestResponders(t *testing.T) {
	tests := []struct {
		name       string
		resp       Responder
		wantStatus int
		wantBody   string
		wantCT     string
	}{
		{"string", StringResponse("hi"), 200, "hi", ""},
		{"status string", StatusStringResponse(404, "nope"), 404, "nope", ""},
		{"json", JSONResponse(map[string]int{"a": 1}), 200, `{"a":1}`, "application/json"},
		{"status json", StatusJSONResponse(401, map[string]string{"message": "x"}), 401, `{"message":"x"}`, "application/json"},
		{"with header", WithHeader(StringResponse("h"), "X-Test", "v"), 200, "h", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &http.Client{}
			reg := ReplaceTripper(c)
			reg.Register(MatchAny, tt.resp)
			resp, got := do(t, c, "GET", "https://dev.azure.com/org")
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := resp.Header.Get("Content-Type"); got != tt.wantCT {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantCT)
			}
			if resp.Request == nil {
				t.Error("response has no Request")
			}
			if got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			if tt.name == "with header" && resp.Header.Get("X-Test") != "v" {
				t.Errorf("X-Test = %q", resp.Header.Get("X-Test"))
			}
		})
	}
}

func TestStubLocations(t *testing.T) {
	c := &http.Client{}
	reg := ReplaceTripper(c)
	reg.StubLocations("org", ResourceAreasLocation, Location{
		ID:            "7a50f0d4-0000-0000-0000-000000000000",
		Area:          "core",
		ResourceName:  "projects",
		RouteTemplate: "_apis/projects/{*projectId}",
	})

	resp, raw := do(t, c, http.MethodOptions, "https://dev.azure.com/org/_apis")
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got struct {
		Count int `json:"count"`
		Value []struct {
			ID              string `json:"id"`
			Area            string `json:"area"`
			ResourceName    string `json:"resourceName"`
			RouteTemplate   string `json:"routeTemplate"`
			ResourceVersion int    `json:"resourceVersion"`
			MinVersion      string `json:"minVersion"`
			MaxVersion      string `json:"maxVersion"`
			ReleasedVersion string `json:"releasedVersion"`
		} `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 2 || len(got.Value) != 2 {
		t.Fatalf("count = %d, len = %d", got.Count, len(got.Value))
	}
	ra := got.Value[0]
	if ra.ID != "e81700f7-3be2-46de-8624-2eb35882fcaa" || ra.ResourceName != "ResourceAreas" || ra.RouteTemplate != "_apis/{resource}/{areaId}" {
		t.Errorf("ResourceAreas location = %+v", ra)
	}
	core := got.Value[1]
	if core.Area != "core" || core.RouteTemplate != "_apis/projects/{*projectId}" {
		t.Errorf("core location = %+v", core)
	}
	// SDK version negotiation dereferences these, so they must always be set.
	if core.MinVersion == "" || core.MaxVersion == "" || core.ReleasedVersion == "" || core.ResourceVersion == 0 {
		t.Errorf("version fields missing: %+v", core)
	}
	reg.Verify(t)
}

func TestStubResourceAreas(t *testing.T) {
	c := &http.Client{}
	reg := ReplaceTripper(c)
	reg.StubResourceAreas("org", ResourceArea{
		ID:          "970aba6c-0000-0000-0000-000000000000",
		Name:        "IMS",
		LocationURL: "https://vssps.dev.azure.com/org/",
	})

	_, raw := do(t, c, "GET", "https://dev.azure.com/org/_apis/ResourceAreas?api-version=5.1-preview.1")
	var got struct {
		Count int `json:"count"`
		Value []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			LocationURL string `json:"locationUrl"`
		} `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || got.Value[0].LocationURL != "https://vssps.dev.azure.com/org/" || got.Value[0].Name != "IMS" {
		t.Errorf("got %+v", got)
	}
	reg.Verify(t)
}

func TestStubIMS(t *testing.T) {
	c := &http.Client{}
	reg := ReplaceTripper(c)
	reg.StubIMS("org")

	_, raw := do(t, c, "GET", "https://dev.azure.com/org/_apis/ResourceAreas")
	var areas collection[ResourceArea]
	if err := json.Unmarshal([]byte(raw), &areas); err != nil {
		t.Fatal(err)
	}
	want := ResourceArea{ID: "8a3d49b8-91f0-46ef-b33d-dda338c25db3", Name: "IMS", LocationURL: "https://vssps.dev.azure.com/org/"}
	if areas.Count != 1 || areas.Value[0] != want {
		t.Errorf("areas = %+v", areas)
	}

	// The identity client's discovery call goes to the vssps host.
	_, raw = do(t, c, http.MethodOptions, "https://vssps.dev.azure.com/org/_apis")
	var locs collection[Location]
	if err := json.Unmarshal([]byte(raw), &locs); err != nil {
		t.Fatal(err)
	}
	wantLoc := Location{ID: "28010c54-d0c0-4c89-a5b0-1c9e188b9fb7", Area: "IMS", ResourceName: "Identities",
		RouteTemplate: "_apis/{resource}/{identityId}"}
	if locs.Count != 1 || locs.Value[0] != wantLoc {
		t.Errorf("locations = %+v", locs)
	}
	reg.Verify(t)
}

func TestGitPRLocations(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range GitPRLocations {
		if seen[l.ID] {
			t.Errorf("duplicate location %s", l.ID)
		}
		seen[l.ID] = true
	}
	for _, id := range []string{
		"9946fd70-0d40-406e-b686-b4744cbbcc37", // pull requests
		"2d874a60-a811-4f62-9c9f-963a6ea0a55b", // refs
		"b5f6bb4f-8d1e-4d79-8d11-4c9172c99c35", // pull request statuses
		"d43911ee-6958-46b0-a42b-8445b8a0d004", // iterations
		"4216bdcf-b6b1-4d59-8b82-c34cc183fc8b", // iteration changes
		"f22387e3-984e-4c52-9c6d-fbb8f14c812d", // labels
		"0a637fcc-5370-4ce8-b0e8-98091f5f9482", // pull request work items
		"fb93c0db-47ed-4a31-8c20-47552878fb44", // items
		"7b28e929-2c99-405d-9c5c-6167a06e6816", // blobs
		"965a3ec7-5ed8-455a-bdcb-835a5ea7fe7b", // thread comments
		"52823034-34a8-4576-922c-8d8b77e9e4c4", // pull request commits
		"72c7ddf8-2cdc-4f60-90cd-ab71c14a399b", // work items (wit update)
	} {
		if !seen[id] {
			t.Errorf("missing location %s", id)
		}
	}
}
