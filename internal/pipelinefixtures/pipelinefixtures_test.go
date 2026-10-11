package pipelinefixtures

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
)

func decodeList[T any](t *testing.T, name string) []T {
	t.Helper()
	var l struct {
		Count int `json:"count"`
		Value []T `json:"value"`
	}
	if err := json.Unmarshal(Read(t, name), &l); err != nil {
		t.Fatal(err)
	}
	if l.Count != len(l.Value) || l.Count == 0 {
		t.Fatalf("%s: count = %d, len(value) = %d", name, l.Count, len(l.Value))
	}
	return l.Value
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

type records struct {
	all  []build.TimelineRecord
	byID map[uuid.UUID]build.TimelineRecord
}

func (r records) ofType(typ string) []build.TimelineRecord {
	var out []build.TimelineRecord
	for _, rec := range r.all {
		if str(rec.Type) == typ {
			out = append(out, rec)
		}
	}
	return out
}

func (r records) parentType(rec build.TimelineRecord) string {
	if rec.ParentId == nil {
		return ""
	}
	return str(r.byID[*rec.ParentId].Type)
}

// TestTimelineFixtures decodes every timeline into the SDK model, checks parent links resolve,
// and pins the shape each file exists to cover.
func TestTimelineFixtures(t *testing.T) {
	tests := map[string]func(t *testing.T, r records){
		"timeline_yaml_stages.json": func(t *testing.T, r records) {
			for typ, parent := range map[string]string{"Stage": "", "Phase": "Stage", "Job": "Phase", "Task": "Job"} {
				recs := r.ofType(typ)
				if len(recs) == 0 {
					t.Errorf("no %s records", typ)
				}
				for _, rec := range recs {
					if got := r.parentType(rec); got != parent {
						t.Errorf("%s %q parent is %q, want %q", typ, str(rec.Name), got, parent)
					}
				}
			}
			var skipped, failed bool
			for _, rec := range r.ofType("Task") {
				if *rec.Result == build.TaskResultValues.Skipped && rec.Log == nil && rec.StartTime == nil {
					skipped = true
				}
				if *rec.Result == build.TaskResultValues.Failed && rec.Issues != nil && len(*rec.Issues) == 2 && *rec.ErrorCount == 1 {
					failed = true
				}
			}
			if !skipped || !failed {
				t.Errorf("skipped task: %v, failed task with error+warning issues: %v", skipped, failed)
			}
		},
		"timeline_default_stage.json": func(t *testing.T, r records) {
			stages := r.ofType("Stage")
			if len(stages) != 1 || str(stages[0].Name) != "__default" || str(stages[0].Identifier) != "__default" {
				t.Errorf("stages = %d, want one __default", len(stages))
			}
			if n := len(r.ofType("Job")); n < 2 {
				t.Errorf("%d jobs, want several under __default", n)
			}
		},
		"timeline_classic.json": func(t *testing.T, r records) {
			if n := len(r.ofType("Stage")); n != 0 {
				t.Errorf("%d Stage records in a classic timeline", n)
			}
			for _, rec := range r.all {
				if (rec.ParentId == nil) != (str(rec.Type) == "Phase") {
					t.Errorf("%s %q: parentId %v", str(rec.Type), str(rec.Name), rec.ParentId)
				}
			}
		},
		"timeline_retried.json": func(t *testing.T, r records) {
			for _, typ := range []string{"Stage", "Phase", "Job"} {
				found := false
				for _, rec := range r.ofType(typ) {
					if *rec.Attempt == 2 && rec.PreviousAttempts != nil && len(*rec.PreviousAttempts) == 1 &&
						*(*rec.PreviousAttempts)[0].Attempt == 1 && *(*rec.PreviousAttempts)[0].RecordId != *rec.Id {
						found = true
					}
				}
				if !found {
					t.Errorf("no retried %s (attempt 2 with one previous attempt)", typ)
				}
			}
		},
		"timeline_checkpoint.json": func(t *testing.T, r records) {
			cps := r.ofType("Checkpoint")
			if len(cps) != 1 || r.parentType(cps[0]) != "Stage" {
				t.Fatalf("want one Checkpoint under a Stage, got %d", len(cps))
			}
			if stage := r.byID[*cps[0].ParentId]; *stage.State != build.TimelineRecordStateValues.Pending || stage.StartTime != nil {
				t.Errorf("gated stage state = %s, start %v", *stage.State, stage.StartTime)
			}
			for _, typ := range []string{"Checkpoint.Approval", "Checkpoint.TaskCheck"} {
				recs := r.ofType(typ)
				if len(recs) != 1 || *recs[0].ParentId != *cps[0].Id || *recs[0].State == build.TimelineRecordStateValues.Completed {
					t.Errorf("want one pending %s under the Checkpoint", typ)
				}
			}
			if a := r.ofType("Checkpoint.Approval"); len(a) == 1 && uuid.MustParse(str(a[0].Identifier)) != *a[0].Id {
				t.Errorf("approval identifier %s != id %s", str(a[0].Identifier), a[0].Id)
			}
		},
		"timeline_in_progress.json": func(t *testing.T, r records) {
			var noLog, running bool
			for _, rec := range r.ofType("Task") {
				noLog = noLog || (*rec.State == build.TimelineRecordStateValues.Completed && rec.Log == nil)
				running = running || *rec.State == build.TimelineRecordStateValues.InProgress
			}
			if !noLog || !running {
				t.Errorf("completed task with log null: %v, running task: %v", noLog, running)
			}
		},
	}
	for name, check := range tests {
		t.Run(name, func(t *testing.T) {
			var tl build.Timeline
			if err := json.Unmarshal(Read(t, name), &tl); err != nil {
				t.Fatal(err)
			}
			r := records{all: *tl.Records, byID: map[uuid.UUID]build.TimelineRecord{}}
			for _, rec := range r.all {
				if _, dup := r.byID[*rec.Id]; dup {
					t.Errorf("duplicate record id %s", rec.Id)
				}
				r.byID[*rec.Id] = rec
			}
			for _, rec := range r.all {
				if rec.ParentId != nil && r.byID[*rec.ParentId].Id == nil {
					t.Errorf("%q has unknown parent %s", str(rec.Name), rec.ParentId)
				}
				if rec.Order == nil || rec.State == nil || rec.Attempt == nil {
					t.Errorf("%q lacks order, state or attempt", str(rec.Name))
				}
				if (rec.Result != nil) != (*rec.State == build.TimelineRecordStateValues.Completed) {
					t.Errorf("%q: state %s with result %v", str(rec.Name), *rec.State, rec.Result)
				}
			}
			check(t, r)
		})
	}
}

func TestDefinitionFixtures(t *testing.T) {
	defs := decodeList[build.BuildDefinition](t, "definitions.json")
	processTypes, queueStatus, folders := map[float64]int{}, map[build.DefinitionQueueStatus]int{}, map[string][]string{}
	completed := 0
	for _, d := range defs {
		p := d.Process.(map[string]any)
		typ := p["type"].(float64)
		processTypes[typ]++
		if (typ == 2) != (p["yamlFilename"] != nil) {
			t.Errorf("%s: process %v", str(d.Name), p)
		}
		queueStatus[*d.QueueStatus]++
		folders[str(d.Name)] = append(folders[str(d.Name)], str(d.Path))
		if d.Repository == nil || str(d.Repository.Type) != "TfsGit" || !strings.HasPrefix(str(d.Repository.DefaultBranch), "refs/heads/") {
			t.Errorf("%s: repository %+v", str(d.Name), d.Repository)
		}
		if d.LatestCompletedBuild != nil && d.LatestCompletedBuild.Result != nil {
			completed++
		}
	}
	if processTypes[1] == 0 || processTypes[2] == 0 {
		t.Errorf("process types = %v, want classic (1) and YAML (2)", processTypes)
	}
	if queueStatus["paused"] == 0 || queueStatus["disabled"] == 0 || queueStatus["enabled"] == 0 {
		t.Errorf("queueStatus = %v", queueStatus)
	}
	if completed == 0 {
		t.Error("no latestCompletedBuild with a result")
	}
	if f := folders["deploy"]; len(f) != 2 || f[0] == f[1] {
		t.Errorf("deploy folders = %v, want two different folders", f)
	}

	var d build.BuildDefinition
	if err := json.Unmarshal(Read(t, "definition_yaml.json"), &d); err != nil {
		t.Fatal(err)
	}
	if d.Revision == nil || d.Variables == nil || d.Repository == nil || str(d.Repository.DefaultBranch) != "refs/heads/main" {
		t.Errorf("definition_yaml.json = %+v", d)
	}
}

func TestArtifactFixtures(t *testing.T) {
	byType := map[string]build.ArtifactResource{}
	for _, a := range decodeList[build.BuildArtifact](t, "artifacts.json") {
		byType[str(a.Resource.Type)] = *a.Resource
	}
	c := byType["Container"]
	if !strings.HasPrefix(str(c.Data), "#/") || !strings.HasSuffix(str(c.DownloadUrl), "&%24format=zip") {
		t.Errorf("Container = %s %s", str(c.Data), str(c.DownloadUrl))
	}
	p := byType["PipelineArtifact"]
	u, err := url.Parse(str(p.DownloadUrl))
	if err != nil || !strings.HasPrefix(u.Host, "artprod") || !strings.HasSuffix(u.Host, ".artifacts.visualstudio.com") ||
		p.Properties == nil || (*p.Properties)["artifactsize"] == "" {
		t.Errorf("PipelineArtifact = %s %v", str(p.DownloadUrl), p.Properties)
	}
	f := byType["FilePath"]
	if !strings.HasPrefix(str(f.DownloadUrl), "file://") || !strings.HasPrefix(str(f.Data), `\\`) {
		t.Errorf("FilePath = %s %s", str(f.Data), str(f.DownloadUrl))
	}
}

func TestErrorFixtures(t *testing.T) {
	for name, typeKey := range map[string]string{
		"error_queue_validation.json":    "BuildRequestValidationFailedException",
		"error_pipeline_validation.json": "PipelineValidationException",
		"error_definition_conflict.json": "DefinitionRevisionConflictException",
	} {
		var e azuredevops.WrappedError
		if err := json.Unmarshal(Read(t, name), &e); err != nil {
			t.Fatal(err)
		}
		if str(e.TypeKey) != typeKey || str(e.Message) == "" {
			t.Errorf("%s: typeKey %q, message %q", name, str(e.TypeKey), str(e.Message))
		}
	}
	var e azuredevops.WrappedError
	if err := json.Unmarshal(Read(t, "error_queue_validation.json"), &e); err != nil {
		t.Fatal(err)
	}
	results, _ := (*e.CustomProperties)["ValidationResults"].([]any)
	if len(results) == 0 || results[0].(map[string]any)["message"] == "" {
		t.Errorf("ValidationResults = %v", results)
	}
}

type fatalRecorder struct {
	testing.TB
	failed bool
}

func (f *fatalRecorder) Helper()      {}
func (f *fatalRecorder) Fatal(...any) { f.failed = true }

func TestReadMissing(t *testing.T) {
	rec := &fatalRecorder{TB: t}
	if Read(rec, "nope.json"); !rec.failed {
		t.Error("Read of a missing file did not fail the test")
	}
}

// TestFixturesScrubbed guards against committing the live org, real GUIDs or identities, and
// checks the README documents every file.
func TestFixturesScrubbed(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(Dir(), "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", Dir(), err)
	}
	readme, err := os.ReadFile(filepath.Join(Dir(), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	guid := regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	email := regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]+`)
	for _, f := range files {
		name := filepath.Base(f)
		b := Read(t, name)
		s := string(b)
		for _, bad := range []string{"snake-in-prod", "descriptor", "uniqueName", "sig=", "\r\n"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
		for _, g := range guid.FindAllString(s, -1) {
			if !strings.HasPrefix(g, "00000000-0000-0000-0000-") {
				t.Errorf("%s contains unscrubbed GUID %s", name, g)
			}
		}
		if e := email.FindString(s); e != "" {
			t.Errorf("%s contains an email-like value %s", name, e)
		}
		if !json.Valid(b) {
			t.Errorf("%s is not valid JSON", name)
		}
		if !strings.Contains(string(readme), "`"+name+"`") {
			t.Errorf("README.md does not document %s", name)
		}
	}
}
