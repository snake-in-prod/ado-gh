// Package browsertest provides a recording browser for tests of commands that open URLs.
package browsertest

// Browser records every URL it is asked to open and returns Err.
type Browser struct {
	URLs []string
	Err  error
}

// Browse records u and returns b.Err.
func (b *Browser) Browse(u string) error {
	b.URLs = append(b.URLs, u)
	return b.Err
}
