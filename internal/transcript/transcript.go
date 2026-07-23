// Package transcript implements the P3 CLI capture half of the session-log
// streaming design (docs/architecture/session-log-streaming-design.md §5 C,
// §6, §8): pipe-pane capture of managed tmux windows, ANSI-stripping, and an
// append-only NDJSON segment spool under .tmux-cli/logs/transcripts/ that the
// redaction/ship pipeline consumes. The segment object, path pattern, and
// rotation/cap semantics are frozen by the P3 transcripts contract.
package transcript

import (
	"path/filepath"
	"strings"
)

// Window kinds — the contract's closed classification set, derived from the
// window-name prefix.
const (
	KindSupervisor = "supervisor"
	KindWorker     = "worker"
	KindDaemon     = "daemon"
	KindOther      = "other"
)

// workerPrefixes are the managed worker-window name prefixes. Goal-namespaced
// variants (execute-g001-1) share the same prefixes.
var workerPrefixes = []string{"execute-", "prereq-", "validator-", "investigator-", "inv-"}

// Kind classifies a window name into the contract's closed
// supervisor|worker|daemon|other set by its name prefix. Goal-namespaced
// supervisors (supervisor-<ns>) classify as supervisor.
func Kind(window string) string {
	switch {
	case window == "supervisor" || strings.HasPrefix(window, "supervisor-"):
		return KindSupervisor
	case window == "taskvisor":
		return KindDaemon
	}
	for _, p := range workerPrefixes {
		if strings.HasPrefix(window, p) {
			return KindWorker
		}
	}
	return KindOther
}

// IsManaged reports whether a window is on the contract's managed-capture list
// (supervisor, taskvisor, execute-*, prereq-*, validator-*, investigator-*,
// inv-*). Exactly the windows whose Kind is not "other".
func IsManaged(window string) bool {
	return Kind(window) != KindOther
}

// Root returns the transcripts spool tree for a project:
// <projectDir>/.tmux-cli/logs/transcripts. It is deliberately SEPARATE from
// the P2 events spool (.tmux-cli/logs/spool stays events-only).
func Root(projectDir string) string {
	return filepath.Join(projectDir, ".tmux-cli", "logs", "transcripts")
}
