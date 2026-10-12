package shared

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// Node is one node of a run tree: a stage, job or task of a build timeline, or the release
// equivalents. It holds no SDK types so the release commands can build one too.
type Node struct {
	// ID is the record ID; Identifier stays the same across attempts (a stage's identifier is
	// its stageRefName).
	ID         string `json:"-"`
	Identifier string `json:"-"`
	Name       string `json:"name"`
	// Type is Stage, Job or Task. The renderer only shows Task nodes on request or when failed.
	Type       string     `json:"type"`
	State      string     `json:"state"`
	Result     string     `json:"result"`
	StartTime  *time.Time `json:"startTime"`
	FinishTime *time.Time `json:"finishTime"`
	Attempt    int        `json:"attempt"`
	LogID      *int       `json:"logId"`
	Issues     []Issue    `json:"issues"`
	Children   []*Node    `json:"children"`
}

// Issue is an error or warning reported on a node.
type Issue struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Annotation is an issue with the names of the nodes leading to the node reporting it.
type Annotation struct {
	Issue Issue
	Path  []string
}

// Pending is a check a stage is waiting for: an approval or another check.
type Pending struct {
	// ID is the approval ID for approvals.
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Stage string `json:"stage"`
	State string `json:"state"`
}

// IsApproval reports whether p is a manual approval.
func (p Pending) IsApproval() bool { return p.Type == "Checkpoint.Approval" }

// Label describes what the stage waits for.
func (p Pending) Label() string {
	if p.IsApproval() {
		return "approval pending"
	}
	return "waiting: " + p.Name
}

// Duration is the node's run time to the second, 0 until it finished.
func (n *Node) Duration() time.Duration {
	if n.StartTime == nil || n.FinishTime == nil {
		return 0
	}
	return n.FinishTime.Sub(*n.StartTime).Round(time.Second)
}

// MarshalJSON adds the duration in seconds and writes empty issues and children as [].
func (n *Node) MarshalJSON() ([]byte, error) {
	type plain Node
	return json.Marshal(struct {
		*plain
		Duration int64   `json:"duration"`
		Issues   []Issue `json:"issues"`
		Children []*Node `json:"children"`
	}{(*plain)(n), int64(n.Duration() / time.Second), orEmpty(n.Issues), orEmpty(n.Children)})
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// PrintTree prints nodes with their mark, attempt and duration, children indented. Tasks are
// shown when verbose; otherwise only failed ones, so failed jobs expand to their failed tasks.
func PrintTree(w io.Writer, cs *iostreams.ColorScheme, nodes []*Node, verbose bool) {
	printTree(w, cs, nodes, verbose, "", "")
}

// printTree is PrintTree with spin, when set, as the mark of in-progress nodes.
func printTree(w io.Writer, cs *iostreams.ColorScheme, nodes []*Node, verbose bool, indent, spin string) {
	for _, n := range nodes {
		if n.Type == "Task" && !verbose && n.Result != "failed" {
			continue
		}
		mark, color := StatusMark(cs, n.State, n.Result)
		if spin != "" && n.State == "inProgress" {
			mark = spin
		}
		line := indent + color(mark) + " " + n.Name
		if n.Attempt > 1 {
			line += fmt.Sprintf(" (attempt %d)", n.Attempt)
		}
		if d := n.Duration(); d > 0 {
			line += cs.Gray(" in " + d.String())
		}
		fmt.Fprintln(w, line)
		printTree(w, cs, n.Children, verbose, indent+"  ", spin)
	}
}

// Annotations collects the issues of nodes and their descendants, depth first. A node's issue
// repeated by a descendant (jobs repeat their tasks' errors) is reported on the descendant only.
func Annotations(nodes []*Node) []Annotation {
	return annotations(nodes, nil)
}

func annotations(nodes []*Node, path []string) []Annotation {
	var out []Annotation
	for _, n := range nodes {
		p := append(slices.Clip(path), n.Name)
		below := annotations(n.Children, p)
		for _, is := range n.Issues {
			if !slices.ContainsFunc(below, func(a Annotation) bool { return a.Issue == is }) {
				out = append(out, Annotation{is, p})
			}
		}
		out = append(out, below...)
	}
	return out
}

// PrintAnnotations prints each annotation's mark and message, then its node path.
func PrintAnnotations(w io.Writer, cs *iostreams.ColorScheme, anns []Annotation) {
	for _, a := range anns {
		mark, color := "!", cs.Yellow
		if a.Issue.Type == "error" {
			mark, color = "X", cs.Red
		}
		fmt.Fprintf(w, "%s %s\n  %s\n", color(mark), a.Issue.Message, strings.Join(a.Path, " / "))
	}
}

// PrintWaiting prints one line per pending check: its mark, stage and label.
func PrintWaiting(w io.Writer, cs *iostreams.ColorScheme, pending []Pending) {
	for _, p := range pending {
		mark, color := StatusMark(cs, p.State, "")
		stage := ""
		if p.Stage != "" {
			stage = p.Stage + ": "
		}
		fmt.Fprintf(w, "%s %s%s\n", color(mark), stage, p.Label())
	}
}
