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
)

// TaskLog is the log of one task, with the stage and job it ran in.
type TaskLog struct {
	// Stage is empty for classic builds and stage-less YAML runs.
	Stage, Job string
	// Index is the task's 1-based position in its job, skipped tasks included.
	Index int
	Task  *Node
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
	var walk func(nodes []*Node, stage, job string, jobMatch bool)
	walk = func(nodes []*Node, stage, job string, jobMatch bool) {
		for i, n := range nodes {
			switch n.Type {
			case "Stage":
				walk(n.Children, n.Name, job, jobMatch)
			case "Job":
				walk(n.Children, stage, n.Name, f.Job == "" || matchesNode(n, f.Job))
			case "Task":
				if jobMatch && n.State == "completed" && n.LogID != nil && *n.LogID != 0 &&
					(f.Task == "" || matchesNode(n, f.Task)) && (!f.Failed || n.Result == "failed") {
					out = append(out, TaskLog{Stage: stage, Job: job, Index: i + 1, Task: n})
				}
			}
		}
	}
	walk(nodes, "", "", f.Job == "")
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

// WriteLogs writes each log to dir/<stage>/<job>/<NN>_<task>.log (no stage level when the run
// has none), creating dir and overwriting earlier files. Names go through SafeName and files
// through CreateFile, so they stay inside dir.
func WriteLogs(ctx context.Context, dir string, open LogOpener, logs []TaskLog) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, l := range logs {
		name := path.Join(SafeName(l.Job), fmt.Sprintf("%02d_%s.log", l.Index, SafeName(l.Task.Name)))
		if l.Stage != "" {
			name = path.Join(SafeName(l.Stage), name)
		}
		if err := withLog(ctx, open, l, func(r io.Reader) error {
			f, err := CreateFile(root, name, os.O_TRUNC)
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
		_, _ = br.Discard(3)
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
