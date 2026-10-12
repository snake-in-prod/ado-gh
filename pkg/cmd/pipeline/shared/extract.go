package shared

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ValidArtifactName reports an error unless name can be used as one directory name on every
// OS: no separators, "." or "..", ":*?\"<>|", control characters, trailing dots or spaces, or
// Windows device names.
func ValidArtifactName(name string) error {
	if name == "" || name != SafeName(name) {
		return fmt.Errorf("artifact %q: name is not a safe directory name", name)
	}
	return nil
}

// zipItem is an archive entry to extract and its path under the target directory, slash-separated
// (name) and in OS form (rel).
type zipItem struct {
	f         *zip.File
	name, rel string
}

// ExtractZip extracts zr into dir, creating dir. When every entry lies in one top-level folder
// named artifact (ignoring case), that folder is stripped. All names are checked with LocalPath
// before anything is written, and writes go through os.Root, so an archive never writes outside
// dir. Symlinks and other non-regular entries are skipped and returned. Each file is capped at
// its declared size. Without clobber, a file that already exists errors before anything is
// written; with it, the file is overwritten. dir itself is never removed.
func ExtractZip(zr *zip.Reader, dir, artifact string, clobber bool) (skipped []string, err error) {
	strip := len(zr.File) > 0
	for _, f := range zr.File {
		first, _, inFolder := strings.Cut(f.Name, "/")
		strip = strip && inFolder && strings.EqualFold(first, artifact)
	}
	var items []zipItem
	for _, f := range zr.File {
		name := strings.TrimSuffix(f.Name, "/")
		rel, err := LocalPath(name)
		if err != nil {
			return nil, fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		if strip {
			_, name, _ = strings.Cut(name, "/")
			if name == "" {
				continue
			}
			rel, _ = LocalPath(name) // a suffix of a local path is local
		}
		if mode := f.Mode(); !mode.IsDir() && !mode.IsRegular() {
			skipped = append(skipped, f.Name)
			continue
		}
		items = append(items, zipItem{f, name, rel})
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	flag := os.O_TRUNC
	if !clobber {
		flag = os.O_EXCL
		for _, it := range items {
			if _, err := root.Lstat(it.rel); err == nil && !it.f.Mode().IsDir() {
				return nil, fmt.Errorf("%s already exists; use --clobber to overwrite it", it.rel)
			}
		}
	}
	for _, it := range items {
		if it.f.Mode().IsDir() {
			err = root.MkdirAll(it.rel, 0o755)
		} else {
			err = extractFile(root, it, flag)
		}
		if err != nil {
			return nil, err
		}
	}
	return skipped, nil
}

// extractFile writes it to root with flag, failing if the entry holds more than its declared size.
func extractFile(root *os.Root, it zipItem, flag int) error {
	rc, err := it.f.Open()
	if err != nil {
		return fmt.Errorf("%s: %w", it.f.Name, err)
	}
	defer rc.Close()
	out, err := CreateFile(root, it.name, flag)
	if err != nil {
		return err
	}
	size := int64(it.f.UncompressedSize64)
	n, err := io.Copy(out, io.LimitReader(rc, size+1))
	if err == nil && n > size {
		err = errors.New("larger than its declared size")
	}
	if err = errors.Join(err, out.Close()); err != nil {
		return fmt.Errorf("%s: %w", it.f.Name, err)
	}
	return nil
}
