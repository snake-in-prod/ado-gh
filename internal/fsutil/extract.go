package fsutil

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// zipItem is an archive entry to extract and its path under the target directory, slash-separated
// (name) and in OS form (rel), and whether it is a directory.
type zipItem struct {
	f         *zip.File
	name, rel string
	dir       bool
}

// slashName is f's name with backslashes turned into slashes.
func slashName(f *zip.File) string { return strings.ReplaceAll(f.Name, `\`, "/") }

// ExtractZip extracts zr into dir, creating dir. When every entry lies in one top-level folder
// named artifact (ignoring case), that folder is stripped. All names are checked with LocalPath
// before anything is written, and writes go through os.Root, so an archive never writes outside
// dir. Symlinks and other non-regular entries are skipped and returned. Each file is capped at
// its declared size (see extractFile). Backslashes in entry names are read as separators: .NET
// on Windows writes them (e.g. ArtifactTool's zip), and Windows extractors treat them so. Without clobber, a file that already exists errors
// before anything is written; with it, the file is overwritten. dir itself is never removed.
func ExtractZip(zr *zip.Reader, dir, artifact string, clobber bool) (skipped []string, err error) {
	strip := len(zr.File) > 0
	for _, f := range zr.File {
		first, _, inFolder := strings.Cut(slashName(f), "/")
		strip = strip && inFolder && strings.EqualFold(first, artifact)
	}
	var items []zipItem
	for _, f := range zr.File {
		name := strings.TrimSuffix(slashName(f), "/")
		rel, err := LocalPath(name)
		if err != nil {
			return nil, fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		if strip {
			_, name, _ = strings.Cut(name, "/")
			if name == "" {
				continue
			}
			if rel, err = LocalPath(name); err != nil {
				return nil, fmt.Errorf("unsafe path in archive: %q", f.Name)
			}
		}
		if mode := f.Mode(); !mode.IsDir() && !mode.IsRegular() {
			skipped = append(skipped, f.Name)
			continue
		}
		// archive/zip only sees a trailing slash as a directory, not a backslash.
		isDir := f.Mode().IsDir() || strings.HasSuffix(slashName(f), "/")
		items = append(items, zipItem{f, name, rel, isDir})
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
			if _, err := root.Lstat(it.rel); err == nil && !it.dir {
				return nil, fmt.Errorf("%s already exists; use --clobber to overwrite it", it.rel)
			}
		}
	}
	for _, it := range items {
		if it.dir {
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

// extractFile writes it to root with flag. The entry can't hold more than its declared size:
// archive/zip's reader fails with zip.ErrFormat past it (and with zip.ErrChecksum on a bad CRC),
// so a lying header can't fill the disk.
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
	_, err = io.Copy(out, rc)
	if err = errors.Join(err, out.Close()); err != nil {
		return fmt.Errorf("%s: %w", it.f.Name, err)
	}
	return nil
}
