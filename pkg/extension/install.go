package extension

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrExecutableNotFound means a local install's directory has no ado-<name> executable yet.
// The link is kept: the author may still build it.
var ErrExecutableNotFound = errors.New("executable not found")

// InstallLocal links root/ado-<name> to the local directory dir: a symlink, or on Windows a
// pointer file. dir is made absolute and must be a directory. An existing entry is refused
// unless force, which removes it (never a local install's target) first. The install record
// has no token consent. When dir has no executable the link is kept and the error is
// ErrExecutableNotFound.
func InstallLocal(root, name, dir string, force bool) (Extension, error) {
	if err := ValidateName(name); err != nil {
		return Extension{}, err
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Extension{}, err
	}
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Extension{}, fmt.Errorf("no such directory: %s", dir)
	}
	if err != nil {
		return Extension{}, err
	}
	if !fi.IsDir() {
		return Extension{}, fmt.Errorf("%s is not a directory", dir)
	}
	if _, err := os.Lstat(filepath.Join(root, Prefix+name)); err == nil {
		if !force {
			return Extension{}, fmt.Errorf("extension %q is already installed; use --force to replace it", name)
		}
		if err := Remove(root, name); err != nil {
			return Extension{}, err
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Extension{}, err
	}
	if err := makeLink(dir, filepath.Join(root, Prefix+name)); err != nil {
		return Extension{}, err
	}
	if err := WriteRecord(root, name, &Record{Source: dir, Kind: KindLocal}); err != nil {
		return Extension{}, err
	}
	ext, err := Lookup(root, name)
	if err != nil {
		return Extension{}, err
	}
	if _, err := os.Stat(ext.Executable()); err != nil {
		return ext, ErrExecutableNotFound
	}
	return ext, nil
}

// Remove deletes the entry of name under root and its install record. A directory is removed
// with its contents; a symlink or pointer file only itself, never its target. A missing entry
// yields an error wrapping fs.ErrNotExist.
func Remove(root, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	path := filepath.Join(root, Prefix+name)
	// Lstat, not Lookup: a link is removed itself, and a broken pointer file is removable.
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		return fmt.Errorf("removing extension %q: %w", name, err)
	}
	if err := os.Remove(RecordPath(root, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the install record of %q: %w", name, err)
	}
	return nil
}

// asidePrefix starts the name a replaced binary is renamed to inside its entry directory.
const asidePrefix = ".old-"

// placeEntry moves the staging directory stage, holding the binary file, to root/ado-<name>.
// An installed binary extension keeps its directory, since Windows can rename a running .exe
// but not delete it or the directory holding it: its old binary is renamed aside, everything
// else in the directory is removed and the new binary is moved in. Aside binaries are removed
// when they can be (on Windows a running one stays until a later install). Any other entry is
// removed first.
func placeEntry(root, name, stage, file string) error {
	path := filepath.Join(root, Prefix+name)
	cur, err := entry(root, name)
	switch {
	case err == nil && cur.Kind == KindBinary:
		return replaceBinary(path, stage, file)
	case err == nil:
		if err := Remove(root, name); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return os.Rename(stage, path)
}

func replaceBinary(dir, stage, file string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, file)
	aside := ""
	for _, e := range entries {
		switch {
		case e.Name() == file:
			aside = filepath.Join(dir, fmt.Sprintf("%s%d-%s", asidePrefix, time.Now().UnixNano(), file))
			if err := os.Rename(dst, aside); err != nil {
				return fmt.Errorf("moving the old binary aside: %w", err)
			}
		case !strings.HasPrefix(e.Name(), asidePrefix):
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(filepath.Join(stage, file), dst); err != nil {
		if aside != "" {
			_ = os.Rename(aside, dst)
		}
		return err
	}
	if entries, err = os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), asidePrefix) {
				_ = os.RemoveAll(filepath.Join(dir, e.Name())) // fails on Windows while it runs
			}
		}
	}
	return nil
}
