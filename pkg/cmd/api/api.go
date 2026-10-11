// Package api implements `ado api`.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/jq"
	"github.com/cli/go-gh/v2/pkg/template"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	f          *cmdutil.Factory
	in         io.Reader
	path       string
	method     string
	methodSet  bool
	headers    []string
	rawFields  []string
	fields     []string
	input      string
	include    bool
	paginate   bool
	jq         string
	template   string
	apiVersion string
}

// NewCmdAPI returns the `api` command.
func NewCmdAPI(f *cmdutil.Factory) *cobra.Command {
	o := &options{f: f}
	cmd := &cobra.Command{
		Use:   "api <path>",
		Short: "Make an authenticated Azure DevOps REST API request",
		Long: `Make an authenticated HTTP request to the Azure DevOps REST API and print the response.

The path is relative to the organization URL (https://dev.azure.com/{org}/), or an absolute
https URL on an Azure DevOps host (dev.azure.com, *.dev.azure.com, *.visualstudio.com).
The placeholders {org}, {project} and {repo} are filled from the current repository
(-R, ADO_REPO or the git remote); {org} also from ADO_ORG.

An api-version query parameter is added when the path has none (see --api-version).

-f/--raw-field adds a string parameter; -F/--field adds a typed one: true, false, null
and integers become JSON values, "@file" reads the value from a file ("@-" from stdin).
Parameters are sent as a JSON object body, which makes the default method POST; with GET
or --input they go to the query string instead.

--paginate (GET only) follows the X-MS-ContinuationToken header or a continuationToken
field in the body, and steps $skip when the path sets $top. Pages that all have a "value"
array are merged into one {"count", "value"} object, so --jq and --template see one result;
other pages are printed one after another.

A non-2xx response prints its body to stderr and exits 1.`,
		Example: `  $ ado api _apis/projects --jq '.value[].name'
  $ ado api '{project}/_apis/git/repositories/{repo}/pullrequests?$top=100' --paginate
  $ ado api -X PATCH '{project}/_apis/wit/workitems/42' --input patch.json \
      -H 'Content-Type: application/json-patch+json'`,
		GroupID: "additional",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.path = args[0]
			o.in = cmd.InOrStdin()
			o.methodSet = cmd.Flags().Changed("method")
			return apiRun(cmd, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.method, "method", "X", "GET", "The HTTP method for the request")
	fl.StringArrayVarP(&o.headers, "header", "H", nil, "Add a HTTP request header in `key:value` format")
	fl.StringArrayVarP(&o.rawFields, "raw-field", "f", nil, "Add a string parameter in `key=value` format")
	fl.StringArrayVarP(&o.fields, "field", "F", nil, "Add a typed parameter in `key=value` format")
	fl.StringVar(&o.input, "input", "", "The `file` to use as body for the HTTP request (use \"-\" to read from standard input)")
	fl.BoolVarP(&o.include, "include", "i", false, "Include HTTP response status line and headers in the output")
	fl.BoolVar(&o.paginate, "paginate", false, "Make additional HTTP requests to fetch all pages of results")
	fl.StringVarP(&o.jq, "jq", "q", "", "Query to select values from the response using jq syntax")
	fl.StringVarP(&o.template, "template", "t", "", "Format JSON output using a Go template")
	fl.StringVar(&o.apiVersion, "api-version", "7.1", "The api-version added when the path has none")
	cmd.MarkFlagsMutuallyExclusive("jq", "template")
	return cmd
}

// page is one response: its -i header block, headers, body, and the body as a JSON object (nil if not one).
type page struct {
	head   string
	header http.Header
	body   []byte
	json   bool
	obj    map[string]any
}

func apiRun(cmd *cobra.Command, o *options) error {
	hasParams := len(o.fields)+len(o.rawFields) > 0
	if !o.methodSet && (hasParams || o.input != "") {
		o.method = "POST"
	}
	o.method = strings.ToUpper(o.method)
	if o.paginate && o.method != http.MethodGet {
		return cmdutil.FlagErrorf("the `--paginate` option is not supported for non-GET requests")
	}
	header := http.Header{}
	for _, h := range o.headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return cmdutil.FlagErrorf("invalid header %q: expected key:value", h)
		}
		header.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	params, err := o.parseFields()
	if err != nil {
		return err
	}

	u, host, org, err := o.resolveURL()
	if err != nil {
		return err
	}
	q := u.Query()
	if !q.Has("api-version") {
		q.Set("api-version", o.apiVersion)
	}

	var body []byte
	switch {
	case o.input != "":
		if body, err = o.readFile(o.input); err != nil {
			return err
		}
	case hasParams && o.method != http.MethodGet:
		if body, err = json.Marshal(params); err != nil {
			return err
		}
	}
	if body == nil || o.input != "" {
		for k, v := range params {
			q.Set(k, queryValue(v))
		}
	}
	if body != nil && header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/json")
	}

	hc, err := shared.HTTPClient(cmd.Context(), o.f, host, org)
	if err != nil {
		return err
	}
	ios := o.f.IOStreams
	var pages []page
	seen := map[string]bool{}
	for {
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(cmd.Context(), o.method, u.String(), bytes.NewReader(body))
		if err != nil {
			return err
		}
		maps.Copy(req.Header, header)
		p, status, err := o.do(hc, req)
		if err != nil {
			return err
		}
		if status > 299 {
			fmt.Fprint(ios.Out, p.head)
			errBody := p.body
			if strings.Contains(p.header.Get("Content-Type"), "text/html") {
				// ADO answers unknown routes with a ~130 KB error page; its title is the message.
				errBody = htmlTitle(p.body)
			}
			fmt.Fprint(ios.ErrOut, string(errBody))
			if len(errBody) > 0 && !bytes.HasSuffix(errBody, []byte("\n")) {
				fmt.Fprintln(ios.ErrOut)
			}
			return fmt.Errorf("HTTP %d: %s (%s)", status, http.StatusText(status), u.Redacted())
		}
		pages = append(pages, p)
		if !o.paginate || !nextPage(q, pages, seen) {
			break
		}
	}
	return o.print(pages)
}

// do sends req and reads the whole response.
func (o *options) do(hc *http.Client, req *http.Request) (page, int, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return page{}, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return page{}, 0, err
	}
	p := page{header: resp.Header, body: b, json: strings.Contains(resp.Header.Get("Content-Type"), "json")}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	_ = dec.Decode(&p.obj) // not a JSON object: obj stays nil
	if o.include {
		var sb strings.Builder
		proto := resp.Proto
		if proto == "" {
			proto = "HTTP/1.1"
		}
		fmt.Fprintf(&sb, "%s %d %s\n", proto, resp.StatusCode, http.StatusText(resp.StatusCode))
		for _, k := range slices.Sorted(maps.Keys(resp.Header)) {
			for _, v := range resp.Header[k] {
				fmt.Fprintf(&sb, "%s: %s\n", k, v)
			}
		}
		sb.WriteString("\n")
		p.head = sb.String()
	}
	return p, resp.StatusCode, nil
}

// nextPage updates q for the page after the last one and reports whether to fetch it:
// the continuation header, else a body continuationToken unless isLastBatch is true or
// hasMore is false, else $skip stepping when the request sets $top, until an empty page.
func nextPage(q url.Values, pages []page, seen map[string]bool) bool {
	last := pages[len(pages)-1]
	tok := last.header.Get(azuredevops.HeaderKeyContinuationToken)
	if s, _ := last.obj["continuationToken"].(string); tok == "" && s != "" &&
		last.obj["isLastBatch"] != true && last.obj["hasMore"] != false {
		tok = s
	}
	if tok != "" {
		if seen[tok] {
			return false
		}
		seen[tok] = true
		q.Set("continuationToken", tok)
		return true
	}
	values, _ := last.obj["value"].([]any)
	if !q.Has("$top") || len(values) == 0 {
		return false
	}
	// Stop when the server ignores $skip and repeats the page.
	if len(pages) > 1 && bytes.Equal(pages[len(pages)-2].body, last.body) {
		return false
	}
	skip, _ := strconv.Atoi(q.Get("$skip"))
	q.Set("$skip", strconv.Itoa(skip+len(values)))
	return true
}

// merge joins the value arrays of pages into one envelope with a recomputed count.
func merge(pages []page) (map[string]any, bool) {
	all := []any{}
	for _, p := range pages {
		v, ok := p.obj["value"].([]any)
		if !ok {
			return nil, false
		}
		all = append(all, v...)
	}
	m := maps.Clone(pages[0].obj)
	delete(m, "continuationToken")
	m["value"] = all
	m["count"] = len(all)
	return m, true
}

func (o *options) print(pages []page) error {
	out := o.f.IOStreams.Out
	if o.paginate {
		if m, ok := merge(pages); ok {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(m); err != nil {
				return err
			}
			for _, p := range pages {
				fmt.Fprint(out, p.head)
			}
			return o.write(buf.Bytes(), true)
		}
	}
	for _, p := range pages {
		fmt.Fprint(out, p.head)
		if err := o.write(p.body, p.json); err != nil {
			return err
		}
	}
	return nil
}

// write prints body through --jq or --template, coloured when it is JSON and color is on.
func (o *options) write(body []byte, isJSON bool) error {
	ios := o.f.IOStreams
	if len(body) == 0 {
		return nil
	}
	switch {
	case o.jq != "":
		indent := ""
		if ios.IsStdoutTTY() {
			indent = "  "
		}
		return jq.EvaluateFormatted(bytes.NewReader(body), ios.Out, o.jq, indent, ios.ColorEnabled())
	case o.template != "":
		t := template.New(ios.Out, ios.TerminalWidth(), ios.ColorEnabled())
		if err := t.Parse(o.template); err != nil {
			return err
		}
		if err := t.Execute(bytes.NewReader(body)); err != nil {
			return err
		}
		return t.Flush()
	case isJSON && ios.ColorEnabled():
		return jq.EvaluateFormatted(bytes.NewReader(body), ios.Out, ".", "  ", true)
	}
	_, err := ios.Out.Write(body)
	return err
}

// resolveURL fills the placeholders and returns the request URL with the host/org to
// authenticate to.
func (o *options) resolveURL() (u *url.URL, host, org string, err error) {
	p := o.path
	// Absolute only when the path starts with a scheme: queries may hold URLs (artifactId=vstfs:///...).
	abs := strings.HasPrefix(p, "https://") || strings.HasPrefix(p, "http://")
	var repo adocontext.Repo
	switch {
	case strings.Contains(p, "{project}") || strings.Contains(p, "{repo}"):
		if repo, err = o.f.BaseRepo(); err != nil {
			return nil, "", "", err
		}
		host, org = repo.Host, repo.Org
	case !abs || strings.Contains(p, "{org}"):
		host, org = shared.OrgFromContext("", o.f.BaseRepo)
		if org == "" {
			return nil, "", "", errors.New("no organization: run inside an Azure DevOps repository, pass -R or set ADO_ORG")
		}
	}
	p = strings.NewReplacer(
		"{org}", url.PathEscape(org),
		"{project}", url.PathEscape(repo.Project),
		"{repo}", url.PathEscape(repo.Name),
	).Replace(p)
	if !abs {
		p = shared.OrgURL(host, org) + strings.TrimPrefix(p, "/")
	}
	u, err = url.Parse(p)
	if err != nil {
		return nil, "", "", err
	}
	if u.Scheme != "https" || !shared.IsADOHost(u.Hostname()) {
		return nil, "", "", fmt.Errorf("not an Azure DevOps URL: %s", u.Redacted())
	}
	if abs {
		host, org = orgFromURL(u)
	}
	return u, host, org, nil
}

// orgFromURL returns the credential host/org of an absolute Azure DevOps URL:
// {org}[.area].visualstudio.com, or the first path segment on *dev.azure.com.
func orgFromURL(u *url.URL) (host, org string) {
	h := strings.ToLower(u.Hostname())
	if strings.HasSuffix(h, ".visualstudio.com") {
		org, _, _ = strings.Cut(h, ".")
		return org + ".visualstudio.com", org
	}
	org, _, _ = strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	return "dev.azure.com", org
}

// parseFields returns the -f and -F parameters.
func (o *options) parseFields() (map[string]any, error) {
	params := map[string]any{}
	for _, f := range o.rawFields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, cmdutil.FlagErrorf("invalid field %q: expected key=value", f)
		}
		params[k] = v
	}
	for _, f := range o.fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, cmdutil.FlagErrorf("invalid field %q: expected key=value", f)
		}
		val, err := o.typedValue(v)
		if err != nil {
			return nil, err
		}
		params[k] = val
	}
	return params, nil
}

func (o *options) typedValue(v string) (any, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n, nil
	}
	if name, ok := strings.CutPrefix(v, "@"); ok {
		b, err := o.readFile(name)
		return string(b), err
	}
	return v, nil
}

// readFile reads name, or stdin for "-".
func (o *options) readFile(name string) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(o.in)
	}
	return os.ReadFile(name)
}

func queryValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

var titleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// htmlTitle returns the unescaped <title> of an HTML page, or nil when it has none.
func htmlTitle(b []byte) []byte {
	m := titleRE.FindSubmatch(b)
	if m == nil {
		return nil
	}
	return []byte(strings.TrimSpace(html.UnescapeString(string(m[1]))))
}
