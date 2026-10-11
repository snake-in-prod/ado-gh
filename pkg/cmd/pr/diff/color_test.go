package diff

import (
	"bytes"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteDiffColors(t *testing.T) {
	in := "diff --git a/x b/x\n" +
		"index 1234567..89abcde 100644\n" +
		"--- a/x\n" +
		"+++ b/x\n" +
		"@@ -1,3 +1,3 @@\n" +
		" ctx\n" +
		"-old\r\n" +
		"+new\n" +
		"\\ No newline at end of file\n" +
		"+last"
	want := "\x1b[1;37mdiff --git a/x b/x\x1b[m\n" +
		"\x1b[1;37mindex 1234567..89abcde 100644\x1b[m\n" +
		"\x1b[1;37m--- a/x\x1b[m\n" +
		"\x1b[1;37m+++ b/x\x1b[m\n" +
		"@@ -1,3 +1,3 @@\n" +
		" ctx\n" +
		"\x1b[31m-old\r\x1b[m\n" +
		"\x1b[32m+new\x1b[m\n" +
		"\\ No newline at end of file\n" +
		"\x1b[32m+last\x1b[m"

	var out bytes.Buffer
	require.NoError(t, writeDiff(&out, []byte(in), true))
	assert.Equal(t, want, out.String())

	out.Reset()
	require.NoError(t, writeDiff(&out, []byte(in), false))
	assert.Equal(t, in, out.String(), "no colour: bytes pass through")
}

func TestUseColor(t *testing.T) {
	tests := []struct {
		mode    string
		enabled bool
		want    bool
		wantErr string
	}{
		{mode: "auto", enabled: true, want: true},
		{mode: "auto", enabled: false, want: false},
		{mode: "always", enabled: false, want: true},
		{mode: "never", enabled: true, want: false},
		{mode: "sometimes", wantErr: `invalid value for --color: "sometimes" (want auto, always or never)`},
	}
	for _, tt := range tests {
		ios, _, _, _ := iostreams.Test()
		ios.SetColorEnabled(tt.enabled)
		got, err := useColor(ios, tt.mode)
		if tt.wantErr != "" {
			assert.EqualError(t, err, tt.wantErr)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, tt.want, got, "%s enabled=%v", tt.mode, tt.enabled)
	}
}

func TestUseColorRespectsNoColor(t *testing.T) {
	t.Setenv("ADO_FORCE_TTY", "1")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	got, err := useColor(iostreams.System(), "auto")
	require.NoError(t, err)
	assert.True(t, got, "TTY colours by default")

	t.Setenv("NO_COLOR", "1")
	got, err = useColor(iostreams.System(), "auto")
	require.NoError(t, err)
	assert.False(t, got, "NO_COLOR disables auto colour")

	got, err = useColor(iostreams.System(), "always")
	require.NoError(t, err)
	assert.True(t, got, "--color always overrides NO_COLOR, like gh")
}
