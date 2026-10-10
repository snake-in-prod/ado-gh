package shared

import (
	"fmt"
	"io"
	"os"
)

// ReadBody reads a pull request description from the file at path, or from in when path is "-".
func ReadBody(in io.Reader, path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(in)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("reading the description: %w", err)
	}
	return string(b), nil
}
