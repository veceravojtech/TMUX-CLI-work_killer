package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedPlanningMode writes .tmux-cli/setting.yaml with the given planning mode
// so EvaluateStartGuard's settings read sees it.
func seedPlanningMode(t *testing.T, root, mode string) {
	t.Helper()
	confDir := filepath.Join(root, ".tmux-cli")
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	content := "taskvisor:\n  planning_mode: " + mode + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "setting.yaml"), []byte(content), 0o644))
}

// TestEvaluateStartGuard locks the single shared start-permission decision used
// by BOTH the MCP taskvisor-start tool and the `tmux-cli taskvisor start` CLI:
// startable work always admits; incremental planning mode bypasses BOTH
// empty-ledger refusals (the daemon authors goal-001 itself); roadmap mode (and
// any unreadable/absent settings) keeps both refusals.
func TestEvaluateStartGuard(t *testing.T) {
	cases := []struct {
		name          string
		mode          string // "" = no setting.yaml at all
		ledgerMissing bool
		hasStartable  bool
		want          StartRefusal
	}{
		{"startable admits regardless of mode", "roadmap", false, true, StartAllowed},
		{"incremental admits missing ledger", "incremental", true, false, StartAllowed},
		{"incremental admits zero startable goals", "incremental", false, false, StartAllowed},
		{"roadmap refuses missing ledger", "roadmap", true, false, StartRefusedNoLedger},
		{"roadmap refuses zero startable goals", "roadmap", false, false, StartRefusedNoStartable},
		{"absent settings default to roadmap refusals", "", true, false, StartRefusedNoLedger},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.mode != "" {
				seedPlanningMode(t, root, tc.mode)
			}
			got := EvaluateStartGuard(root, tc.ledgerMissing, tc.hasStartable)
			require.Equal(t, tc.want, got)
		})
	}
}
