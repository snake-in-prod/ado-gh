package shared

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/fsutil"
)

// TaskLog is the log of one task, with the stage and job it ran in.
type TaskLog struct {
	// Stage is empty for classic builds and stage-less YAML runs.
	Stage, Job string
	// Index is the task's 1-based position in its job, skipped tasks included.
	Index int
	Task  *Node
	// JobNode is the job the task ran in (nil for a task outside any job); it tells apart jobs
	// that share a name.
	JobNode *Node
}

// LogFilter selects task logs. Job and Task match a name (case-insensitive) or record ID.
type LogFilter struct {
	Job, Task string
	Failed    bool
}

// LogOpener opens a task's log for streaming.
type LogOpener func(context.Context, TaskLog) (io.ReadCloser, error)

// BuildLogOpener opens the logs of run buildID by the task record's log.id, as plain text.
func BuildLogOpener(c *api.PipelineClient, buildID int) LogOpener {
	return func(ctx context.Context, l TaskLog) (io.ReadCloser, error) {
		return c.BuildLog(ctx, buildID, *l.Task.LogID)
	}
}

// TaskLogs returns the logs of the completed tasks under nodes matching f, in tree order. Only
// Task records count: job and phase logs repeat their tasks' output. Log id 0 is the
// placeholder agentless tasks report.
func TaskLogs(nodes []*Node, f LogFilter) []TaskLog {
	var out []TaskLog
	var walk func(nodes []*Node, stage string, job *Node, jobMatch bool)
	walk = func(nodes []*Node, stage string, job *Node, jobMatch bool) {
		for i, n := range nodes {
			switch n.Type {
			case "Stage":
				walk(n.Children, n.Name, job, jobMatch)
			case "Job":
				walk(n.Children, stage, n, f.Job == "" || matchesNode(n, f.Job))
			case "Task":
				if jobMatch && n.State == "completed" && n.LogID != nil && *n.LogID != 0 &&
					(f.Task == "" || matchesNode(n, f.Task)) && (!f.Failed || n.Result == "failed") {
					l := TaskLog{Stage: stage, Index: i + 1, Task: n, JobNode: job}
					if job != nil {
						l.Job = job.Name
					}
					out = append(out, l)
				}
			}
		}
	}
	walk(nodes, "", nil, f.Job == "")
	return out
}

func matchesNode(n *Node, ref string) bool {
	return strings.EqualFold(n.Name, ref) || strings.EqualFold(n.ID, ref) ||
		(n.Identifier != "" && strings.EqualFold(n.Identifier, ref))
}

// PrintLogs streams each log to w, every line prefixed "job<TAB>task<TAB>" as gh does. Lines of
// any length stream through without being buffered whole.
func PrintLogs(ctx context.Context, w io.Writer, open LogOpener, logs []TaskLog) error {
	for _, l := range logs {
		if err := withLog(ctx, open, l, func(r io.Reader) error {
			return prefixLines(w, r, l.Job+"\t"+l.Task.Name+"\t")
		}); err != nil {
			return err
		}
	}
	return nil
}

// WriteLogs writes logs under dir with a new LogWriter.
func WriteLogs(ctx context.Context, dir string, open LogOpener, logs []TaskLog) error {
	return (&LogWriter{Dir: dir}).Write(ctx, open, logs)
}

// LogWriter writes task logs to Dir/<stage>/<job>/<NN>_<task>.log (no stage level when the run
// has none), creating Dir and overwriting earlier files. Names go through SafeName and files
// through CreateFile, so they stay inside Dir. Each job gets its own directory: a job whose
// directory another job already took (same display name, names SafeName maps alike, or names
// differing only in case) gets a "_2", "_3", ... suffix. Reuse one LogWriter across calls (as
// --follow does) so a job keeps its directory.
type LogWriter struct {
	Dir string
	// dirs maps each job to its directory; taken holds the directories in use, lower-cased.
	dirs  map[jobKey]string
	taken map[string]bool
}

// jobKey identifies a job across polls: its record ID (a fresh tree has new nodes), else its
// node, plus its stage and name.
type jobKey struct {
	id         string
	node       *Node
	stage, job string
}

// Write writes logs, opening each with open.
func (w *LogWriter) Write(ctx context.Context, open LogOpener, logs []TaskLog) error {
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(w.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, l := range logs {
		name := path.Join(w.jobDir(l), fmt.Sprintf("%02d_%s.log", l.Index, fsutil.SafeName(l.Task.Name)))
		if err := withLog(ctx, open, l, func(r io.Reader) error {
			f, err := fsutil.CreateFile(root, name, os.O_TRUNC)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, r)
			return errors.Join(err, f.Close())
		}); err != nil {
			return err
		}
	}
	return nil
}

// jobDir returns the directory of l's job, assigning the job a free one on first use.
func (w *LogWriter) jobDir(l TaskLog) string {
	key := jobKey{stage: l.Stage, job: l.Job}
	switch {
	case l.JobNode != nil && l.JobNode.ID != "":
		key.id = l.JobNode.ID
	case l.JobNode != nil:
		key.node = l.JobNode
	}
	if d, ok := w.dirs[key]; ok {
		return d
	}
	if w.dirs == nil {
		w.dirs, w.taken = map[jobKey]string{}, map[string]bool{}
	}
	base := fsutil.SafeName(l.Job)
	if l.Stage != "" {
		base = path.Join(fsutil.SafeName(l.Stage), base)
	}
	d := base
	for n := 2; w.taken[strings.ToLower(d)]; n++ {
		d = fmt.Sprintf("%s_%d", base, n)
	}
	w.taken[strings.ToLower(d)] = true
	w.dirs[key] = d
	return d
}

// withLog opens l's log, hands it to use and closes it, naming the job and task on error.
func withLog(ctx context.Context, open LogOpener, l TaskLog, use func(io.Reader) error) error {
	rc, err := open(ctx, l)
	if err == nil {
		err = use(rc)
		rc.Close()
	}
	if err != nil {
		return fmt.Errorf("log of %s / %s: %w", l.Job, l.Task.Name, err)
	}
	return nil
}

// prefixLines copies r to w with prefix before every line, dropping a leading UTF-8 BOM and
// ending an unterminated last line with "\n".
func prefixLines(w io.Writer, r io.Reader, prefix string) error {
	br := bufio.NewReader(r)
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte("\ufeff")) {
		if _, err := br.Discard(3); err != nil {
			return err
		}
	}
	atStart := true
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			if atStart {
				if _, werr := io.WriteString(w, prefix); werr != nil {
					return werr
				}
			}
			if _, werr := w.Write(chunk); werr != nil {
				return werr
			}
			atStart = chunk[len(chunk)-1] == '\n'
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			if !atStart {
				_, err := io.WriteString(w, "\n")
				return err
			}
			return nil
		case err != nil:
			return err
		}
	}
}
