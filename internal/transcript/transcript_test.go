package transcript

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKind_ClassifiesByWindowPrefix(t *testing.T) {
	cases := map[string]string{
		"supervisor":      KindSupervisor,
		"supervisor-g001": KindSupervisor,
		"taskvisor":       KindDaemon,
		"execute-1":       KindWorker,
		"execute-g001-2":  KindWorker,
		"prereq-1":        KindWorker,
		"validator-g001":  KindWorker,
		"investigator-1":  KindWorker,
		"inv-2":           KindWorker,
		"web":             KindOther,
		"invalid":         KindOther, // "inv-" prefix requires the dash
		"supervisorish":   KindOther,
		"taskvisor-2":     KindOther,
		"":                KindOther,
	}
	for window, want := range cases {
		assert.Equal(t, want, Kind(window), "window %q", window)
	}
}

func TestIsManaged_MatchesContractList(t *testing.T) {
	for _, w := range []string{"supervisor", "supervisor-g001", "taskvisor", "execute-1", "prereq-2", "validator-g001", "investigator-1", "inv-3"} {
		assert.True(t, IsManaged(w), "window %q must be managed", w)
	}
	for _, w := range []string{"web", "htop", "supervisorish", ""} {
		assert.False(t, IsManaged(w), "window %q must NOT be managed", w)
	}
}

func TestRoot_IsSeparateFromEventsSpool(t *testing.T) {
	root := Root("/proj")
	assert.Equal(t, filepath.Join("/proj", ".tmux-cli", "logs", "transcripts"), root)
	assert.NotContains(t, root, "spool", "transcripts tree must be separate from the P2 events spool")
}
