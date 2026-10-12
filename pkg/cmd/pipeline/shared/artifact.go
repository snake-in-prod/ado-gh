package shared

import (
	"fmt"

	"github.com/snake-in-prod/ado-gh/internal/fsutil"
)

// ValidArtifactName reports an error unless name can be used as one directory name on every
// OS: no separators, "." or "..", ":*?\"<>|", control characters, trailing dots or spaces, or
// Windows device names.
func ValidArtifactName(name string) error {
	if name == "" || name != fsutil.SafeName(name) {
		return fmt.Errorf("artifact %q: name is not a safe directory name", name)
	}
	return nil
}
