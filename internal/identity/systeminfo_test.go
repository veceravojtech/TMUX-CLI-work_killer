package identity

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTmuxVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "stable release", in: "tmux 3.4\n", want: "3.4"},
		{name: "next build", in: "tmux next-3.5\n", want: "next-3.5"},
		{name: "empty", in: "", want: ""},
		{name: "garbage", in: "garbage", want: ""},
		{name: "prefix only no space", in: "tmux", want: ""},
		{name: "extra whitespace", in: "  tmux 3.4  \n", want: "3.4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseTmuxVersion(tc.in))
		})
	}
}

func TestCollectSystemInfo_Fields(t *testing.T) {
	info := CollectSystemInfo("1.2.3")
	assert.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, info.OS)
	assert.Equal(t, runtime.Version(), info.GoVersion)
	assert.Equal(t, "1.2.3", info.CLIVersion)
	assert.Equal(t, Fingerprint(), info.Fingerprint)
	assert.Equal(t, os.Getenv("SHELL"), info.Shell)
	// Hostname and Username may be empty in a sandbox; assert they equal their
	// helper outputs rather than asserting non-emptiness.
	assert.Equal(t, hostname(), info.Hostname)
	assert.Equal(t, currentUsername(), info.Username)
}
