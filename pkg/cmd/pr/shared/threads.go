package shared

import (
	"slices"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

// VisibleThreads drops deleted threads, system threads (votes, pushes, merge attempts: any
// thread with a CodeReviewThreadType property), deleted and system comments, then threads left
// without comments. It reuses the threads' comment slices.
func VisibleThreads(all []git.GitPullRequestCommentThread) []git.GitPullRequestCommentThread {
	out := []git.GitPullRequestCommentThread{}
	for _, t := range all {
		if Deref(t.IsDeleted) || t.Comments == nil || systemThread(t) {
			continue
		}
		comments := slices.DeleteFunc(*t.Comments, func(c git.Comment) bool {
			return Deref(c.IsDeleted) || c.CommentType != nil && *c.CommentType == git.CommentTypeValues.System
		})
		if len(comments) > 0 {
			t.Comments = &comments
			out = append(out, t)
		}
	}
	return out
}

func systemThread(t git.GitPullRequestCommentThread) bool {
	props, _ := t.Properties.(map[string]any)
	_, ok := props["CodeReviewThreadType"]
	return ok
}

// FilePath is the file a thread is anchored to, "" for a pull request level thread.
func FilePath(t git.GitPullRequestCommentThread) string {
	if t.ThreadContext == nil {
		return ""
	}
	return Deref(t.ThreadContext.FilePath)
}

// ThreadLines returns the line range a thread is anchored to: the right (new) side when set,
// else the left (old) side. Both are 0 for a pull request level thread; end is start when the
// thread has no end position.
func ThreadLines(t git.GitPullRequestCommentThread) (start, end int) {
	tc := t.ThreadContext
	if tc == nil {
		return 0, 0
	}
	from, to := tc.RightFileStart, tc.RightFileEnd
	if from == nil || Deref(from.Line) == 0 {
		from, to = tc.LeftFileStart, tc.LeftFileEnd
	}
	if from == nil || Deref(from.Line) == 0 {
		return 0, 0
	}
	start = Deref(from.Line)
	end = start
	if to != nil && Deref(to.Line) > start {
		end = Deref(to.Line)
	}
	return start, end
}

// ChangeKinds splits a change type flags value such as "rename, edit" into lowercase kinds.
func ChangeKinds(t *git.VersionControlChangeType) []string {
	var out []string
	if t != nil {
		for k := range strings.SplitSeq(string(*t), ",") {
			out = append(out, strings.ToLower(strings.TrimSpace(k)))
		}
	}
	return out
}
