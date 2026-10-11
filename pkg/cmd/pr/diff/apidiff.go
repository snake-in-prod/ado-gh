package diff

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	udiff "github.com/aymanbagabas/go-udiff"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

const (
	// maxBlobSize is the blob size above which a file gets a notice instead of a diff, like the
	// web Files tab truncating files over 5 MB.
	maxBlobSize = 5 << 20
	// apiWorkers is how many files download at once.
	apiWorkers = 4
	// binarySniff is how many leading bytes git checks for a NUL to call a file binary.
	binarySniff = 8000
	nullID      = "0000000"
)

// fileChange is one changed file of an iteration; paths have no leading "/".
type fileChange struct {
	kinds                      []string
	path, oldPath              string
	objectID, originalObjectID string
}

func (c fileChange) is(kind string) bool { return slices.Contains(c.kinds, kind) }

// apiDiff writes pull request pr's diff built from the REST API, for use outside a clone: the
// latest iteration's changes against its common commit (the web Files tab), each file diffed
// from its blobs with git-style headers, or only the changed names with nameOnly.
func apiDiff(ctx context.Context, f *cmdutil.Factory, c *api.PRClient, pr *git.GitPullRequest, nameOnly, patch, color bool) error {
	if patch && !nameOnly {
		return errors.New("--patch needs a local clone of the repository; run it inside a clone of the pull request's repository")
	}
	id := *pr.PullRequestId
	its, err := c.Iterations(ctx, id)
	if err != nil {
		return err
	}
	if len(its) == 0 {
		return fmt.Errorf("pull request !%d has no iterations", id)
	}
	it := slices.MaxFunc(its, func(a, b git.GitPullRequestIteration) int { return cmp.Compare(intOr0(a.Id), intOr0(b.Id)) })
	raw, err := c.IterationChanges(ctx, id, intOr0(it.Id))
	if err != nil {
		return err
	}
	changes := parseChanges(raw)

	if nameOnly {
		var b strings.Builder
		for _, ch := range changes {
			b.WriteString(ch.path + "\n")
		}
		_, err := f.IOStreams.Out.Write([]byte(b.String()))
		return err
	}

	// Old sides live in the target repository; a fork's new sides only in the fork, addressed
	// by ID alone since it may sit in another project.
	newSide := c
	if fs := pr.ForkSource; fs != nil && fs.Repository != nil && fs.Repository.Id != nil {
		newSide = &api.PRClient{Git: c.Git, Repo: fs.Repository.Id.String()}
	}
	base := cmp.Or(commitID(it.CommonRefCommit), commitID(it.TargetRefCommit))

	out := make([][]byte, len(changes))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		once     sync.Once
		firstErr error
		wg       sync.WaitGroup
	)
	jobs := make(chan int)
	for range min(apiWorkers, len(changes)) {
		wg.Go(func() {
			for i := range jobs {
				var err error
				if out[i], err = fileDiff(ctx, c, newSide, base, changes[i]); err != nil {
					once.Do(func() { firstErr = err; cancel() })
				}
			}
		})
	}
feed:
	for i := range changes {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return writeDiff(f.IOStreams.Out, bytes.Join(out, nil), color)
}

// parseChanges turns iteration changes into files; folders and entries without a path are
// skipped. changeType is a flags value such as "rename, edit".
func parseChanges(raw []git.GitPullRequestChange) []fileChange {
	var out []fileChange
	for _, ch := range raw {
		item, _ := ch.Item.(map[string]any)
		str := func(k string) string { s, _ := item[k].(string); return s }
		path := strings.TrimPrefix(str("path"), "/")
		if path == "" || item["isFolder"] == true || cmp.Or(str("gitObjectType"), "blob") != "blob" {
			continue
		}
		fc := fileChange{path: path, oldPath: path, objectID: str("objectId"), originalObjectID: str("originalObjectId")}
		if ch.OriginalPath != nil && *ch.OriginalPath != "" {
			fc.oldPath = strings.TrimPrefix(*ch.OriginalPath, "/")
		}
		if ch.ChangeType != nil {
			for k := range strings.SplitSeq(string(*ch.ChangeType), ",") {
				fc.kinds = append(fc.kinds, strings.ToLower(strings.TrimSpace(k)))
			}
		}
		out = append(out, fc)
	}
	// git prints files in byte order of their paths; ADO returns its own order.
	slices.SortStableFunc(out, func(a, b fileChange) int { return strings.Compare(a.path, b.path) })
	return out
}

// fileDiff renders one file like git diff: header, then hunks, a binary line or a too-large
// notice. Mode is always 100644 (ADO exposes none) and renames carry no similarity index.
func fileDiff(ctx context.Context, oldSide, newSide *api.PRClient, base string, ch fileChange) ([]byte, error) {
	add, del, rename := ch.is("add"), ch.is("delete"), ch.is("rename")
	a, b := "a/"+ch.oldPath, "b/"+ch.path
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "diff --git %s %s\n", a, b)
	switch {
	case add:
		buf.WriteString("new file mode 100644\n")
	case del:
		buf.WriteString("deleted file mode 100644\n")
	case rename:
		fmt.Fprintf(&buf, "rename from %s\nrename to %s\n", ch.oldPath, ch.path)
		if !ch.is("edit") {
			return buf.Bytes(), nil
		}
	}

	var old, cur blob
	var err error
	if !add {
		// A deleted file's only blob may come as objectId.
		oldID := ch.originalObjectID
		if oldID == "" && del {
			oldID = ch.objectID
		}
		if old, err = loadBlob(ctx, oldSide, oldID, ch.oldPath, base); err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", ch.oldPath, err)
		}
	}
	if !del {
		if cur, err = loadBlob(ctx, newSide, ch.objectID, "", ""); err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", ch.path, err)
		}
	}

	oldLabel, newLabel := a, b
	switch {
	case add:
		oldLabel = "/dev/null"
		fmt.Fprintf(&buf, "index %s..%s\n", nullID, short(cur.id))
	case del:
		newLabel = "/dev/null"
		fmt.Fprintf(&buf, "index %s..%s\n", short(old.id), nullID)
	default:
		fmt.Fprintf(&buf, "index %s..%s 100644\n", short(old.id), short(cur.id))
	}
	switch {
	case old.tooLarge || cur.tooLarge:
		buf.WriteString("file too large to diff (over 5 MB)\n")
	case isBinary(old.data) || isBinary(cur.data):
		fmt.Fprintf(&buf, "Binary files %s and %s differ\n", oldLabel, newLabel)
	default:
		// Empty when the contents match, such as an empty new file: headers only, like git.
		buf.WriteString(udiff.Unified(oldLabel, newLabel, string(old.data), string(cur.data)))
	}
	return buf.Bytes(), nil
}

type blob struct {
	id       string
	data     []byte
	tooLarge bool
}

// loadBlob reads blob id after checking its size, or without an id the file at path as of
// commit (whose blob ID is then hashed locally, as git would).
func loadBlob(ctx context.Context, c *api.PRClient, id, path, commit string) (blob, error) {
	if id == "" {
		data, err := c.ItemContentAt(ctx, "/"+path, commit)
		if err != nil {
			return blob{}, err
		}
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", len(data))
		h.Write(data)
		return blob{id: hex.EncodeToString(h.Sum(nil)), data: data, tooLarge: len(data) > maxBlobSize}, nil
	}
	meta, err := c.Blob(ctx, id)
	if err != nil {
		return blob{}, err
	}
	if meta.Size != nil && *meta.Size > maxBlobSize {
		return blob{id: id, tooLarge: true}, nil
	}
	if meta.Size != nil && *meta.Size == 0 {
		return blob{id: id}, nil
	}
	data, err := c.BlobContent(ctx, id)
	return blob{id: id, data: data}, err
}

// isBinary applies git's rule: a NUL byte in the first 8000 bytes.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binarySniff)], 0) >= 0
}

// short abbreviates an object ID like git: 7 characters, lowercase (ADO returns uppercase).
func short(id string) string {
	return strings.ToLower(id[:min(len(id), 7)])
}
