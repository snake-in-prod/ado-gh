package shared

import (
	"cmp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
)

// Timeline is a build timeline as a tree, with the checks its stages wait for.
type Timeline struct {
	Nodes   []*Node
	Pending []Pending
}

// defaultStage is the identifier of the implicit stage of a YAML pipeline without stages.
const defaultStage = "__default"

// NewTimeline rebuilds the tree of a build timeline's flat records from their parentId,
// siblings sorted by order. Records whose parent is missing are roots (classic builds have no
// stages). Phase levels are collapsed and the implicit __default stage is hidden, so its jobs are
// roots like a classic build's. Of the records sharing a parent and identifier (name when
// unset), only those of the highest attempt are kept. Checkpoint records are left out of the
// tree; the non-completed Checkpoint.* ones become Pending.
func NewTimeline(records []build.TimelineRecord) Timeline {
	ids := map[uuid.UUID]bool{}
	for _, r := range records {
		if r.Id != nil {
			ids[*r.Id] = true
		}
	}
	children := map[uuid.UUID][]build.TimelineRecord{}
	for _, r := range records {
		if r.Id == nil {
			continue
		}
		var parent uuid.UUID
		if r.ParentId != nil && ids[*r.ParentId] {
			parent = *r.ParentId
		}
		children[parent] = append(children[parent], r)
	}

	var tl Timeline
	var walk func(parent uuid.UUID, stage string) []*Node
	walk = func(parent uuid.UUID, stage string) []*Node {
		var out []*Node
		for _, r := range latestAttempts(children[parent]) {
			typ := prshared.Deref(r.Type)
			switch {
			case strings.HasPrefix(typ, "Checkpoint"):
				state := string(prshared.Deref(r.State))
				if typ != "Checkpoint" && state != string(build.TimelineRecordStateValues.Completed) {
					tl.Pending = append(tl.Pending, Pending{
						ID: cmp.Or(prshared.Deref(r.Identifier), r.Id.String()), Type: typ,
						Name: prshared.Deref(r.Name), Stage: stage, State: state,
					})
				}
				walk(*r.Id, stage)
			case typ == "Phase":
				jobs := walk(*r.Id, stage)
				if len(jobs) == 0 {
					// A phase gets its Job record only once it starts (e.g. after an approval),
					// so until then the phase stands in for its job.
					n := nodeOf(r)
					n.Type = "Job"
					jobs = []*Node{n}
				}
				out = append(out, jobs...)
			case typ == "Stage" && recordKey(r) == defaultStage:
				out = append(out, walk(*r.Id, "")...)
			default:
				n := nodeOf(r)
				childStage := stage
				if typ == "Stage" {
					childStage = n.Name
				}
				n.Children = walk(*r.Id, childStage)
				out = append(out, n)
			}
		}
		return out
	}
	tl.Nodes = walk(uuid.Nil, "")
	return tl
}

// recordKey identifies a record across attempts.
func recordKey(r build.TimelineRecord) string {
	return cmp.Or(prshared.Deref(r.Identifier), prshared.Deref(r.Name))
}

// latestAttempts drops the records of older attempts and sorts the rest by order. Records of the
// same attempt sharing a key (same-named tasks have no identifier) are distinct and all kept.
func latestAttempts(rs []build.TimelineRecord) []build.TimelineRecord {
	top := map[string]int{}
	for _, r := range rs {
		top[recordKey(r)] = max(top[recordKey(r)], prshared.Deref(r.Attempt))
	}
	out := slices.DeleteFunc(slices.Clone(rs), func(r build.TimelineRecord) bool {
		return prshared.Deref(r.Attempt) < top[recordKey(r)]
	})
	slices.SortStableFunc(out, func(a, b build.TimelineRecord) int {
		return cmp.Compare(prshared.Deref(a.Order), prshared.Deref(b.Order))
	})
	return out
}

func nodeOf(r build.TimelineRecord) *Node {
	n := &Node{
		ID: r.Id.String(), Identifier: prshared.Deref(r.Identifier), Name: prshared.Deref(r.Name),
		Type: prshared.Deref(r.Type), State: string(prshared.Deref(r.State)), Result: string(prshared.Deref(r.Result)),
		StartTime: api.TimeOf(r.StartTime), FinishTime: api.TimeOf(r.FinishTime), Attempt: prshared.Deref(r.Attempt),
	}
	if r.Log != nil {
		n.LogID = r.Log.Id
	}
	for _, is := range prshared.Deref(r.Issues) {
		n.Issues = append(n.Issues, Issue{string(prshared.Deref(is.Type)), prshared.Deref(is.Message)})
	}
	return n
}
