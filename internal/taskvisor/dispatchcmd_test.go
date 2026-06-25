package taskvisor

import "testing"

// TestDispatchCommand pins the EXACT slash command taskvisor sends for every
// dispatch kind. This is the control surface for "what taskvisor dispatches" —
// if a string here changes, it must change deliberately, with this test updated
// in the same commit.
func TestDispatchCommand(t *testing.T) {
	args := DispatchArgs{
		DispatchPath: "/work/.tmux-cli/goals/goal-001/dispatch.md",
		GoalMdPath:   "/work/.tmux-cli/goals/goal-001/goal.md",
		GoalID:       "goal-001",
		Prompt:       "run the nightly sweep",
	}
	tests := []struct {
		name string
		kind DispatchKind
		want string
	}{
		{
			name: "plan carries dispatch path and id",
			kind: DispatchPlan,
			want: "/tmux:plan /work/.tmux-cli/goals/goal-001/dispatch.md goal-001",
		},
		{
			name: "implement carries id only (no dispatch path)",
			kind: DispatchImplement,
			want: "/tmux:supervisor goal-001",
		},
		{
			name: "elaborate carries dispatch path and id",
			kind: DispatchElaborate,
			want: "/tmux:elaborate /work/.tmux-cli/goals/goal-001/dispatch.md goal-001",
		},
		{
			name: "investigate carries goal.md path",
			kind: DispatchInvestigate,
			want: "/tmux:investigate /work/.tmux-cli/goals/goal-001/goal.md",
		},
		{
			name: "recurring supervisor carries the free-form prompt",
			kind: DispatchRecurringSupervisor,
			want: "/tmux:supervisor run the nightly sweep",
		},
		{
			name: "gate carries id only (no dispatch path)",
			kind: DispatchGate,
			want: "/tmux:gate goal-001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dispatchCommand(tt.kind, args); got != tt.want {
				t.Fatalf("dispatchCommand(%s) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestDispatchImplementIgnoresPath documents that the supervisor-retry command
// must NOT leak a dispatch path — only the authoritative goal id is shipped.
func TestDispatchImplementIgnoresPath(t *testing.T) {
	got := dispatchCommand(DispatchImplement, DispatchArgs{
		DispatchPath: "/some/other/path.md",
		GoalMdPath:   "/another/goal.md",
		GoalID:       "goal-042",
	})
	if want := "/tmux:supervisor goal-042"; got != want {
		t.Fatalf("dispatchCommand(implement) = %q, want %q", got, want)
	}
}

// TestDispatchKindString keeps the human-readable kind names stable for logs.
func TestDispatchKindString(t *testing.T) {
	tests := []struct {
		kind DispatchKind
		want string
	}{
		{DispatchPlan, "plan"},
		{DispatchImplement, "implement"},
		{DispatchElaborate, "elaborate"},
		{DispatchInvestigate, "investigate"},
		{DispatchRecurringSupervisor, "recurring-supervisor"},
		{DispatchGate, "gate"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("DispatchKind(%d).String() = %q, want %q", int(tt.kind), got, tt.want)
		}
	}
}

// TestInitialDispatchKind pins the PHASE → first-dispatch-command matrix. This
// is the control surface the user drives: which phases skip planning.
func TestInitialDispatchKind(t *testing.T) {
	tests := []struct {
		phase string
		want  DispatchKind
	}{
		{PhaseGate, DispatchGate}, // gate gets its own quick executor (no plan, no supervisor)
		{PhaseScaffold, DispatchPlan},
		{PhaseFixtures, DispatchPlan},
		{PhaseDomain, DispatchPlan},
		{PhaseApplication, DispatchPlan},
		{PhaseInfrastructure, DispatchPlan},
		{PhaseAction, DispatchPlan},
		{PhaseAuth, DispatchPlan},
		{PhaseEvent, DispatchPlan},
		{PhaseCrossCutting, DispatchPlan},
		{PhaseDeployment, DispatchPlan},
		{PhaseCI, DispatchPlan},
		{PhaseFinal, DispatchPlan},
		{"", DispatchPlan},                // empty → safe full default
		{"totally-unknown", DispatchPlan}, // unknown → safe full default
	}
	for _, tt := range tests {
		if got := initialDispatchKind(tt.phase); got != tt.want {
			t.Errorf("initialDispatchKind(%q) = %s, want %s", tt.phase, got, tt.want)
		}
	}
}

// TestGatePhaseUsesDedicatedExecutor is the headline guarantee: a gate goal
// dispatches the dedicated, quick /tmux:gate executor — NEVER the /tmux:plan
// pre-planner and NEVER the /tmux:supervisor implementation orchestrator.
func TestGatePhaseUsesDedicatedExecutor(t *testing.T) {
	kind := initialDispatchKind(PhaseGate)
	if kind != DispatchGate {
		t.Fatalf("gate phase kind = %s, want gate (no plan, no supervisor)", kind)
	}
	cmd := dispatchCommand(kind, DispatchArgs{
		DispatchPath: "/work/.tmux-cli/goals/goal-001/dispatch.md",
		GoalID:       "goal-001",
	})
	if want := "/tmux:gate goal-001"; cmd != want {
		t.Fatalf("gate dispatch command = %q, want %q (not plan, not supervisor)", cmd, want)
	}
}

// TestInitialDispatchMatrixCoversAllowedPhases guards drift: every phase
// goal-create accepts must have an explicit matrix row, so a newly-added phase
// can't silently inherit the default. The list mirrors allowedPhases in
// internal/mcp/tools_taskvisor.go (kept in sync by hand — taskvisor cannot
// import mcp without an import cycle).
func TestInitialDispatchMatrixCoversAllowedPhases(t *testing.T) {
	allowed := []string{
		"gate", "scaffold", "fixtures", "domain", "application",
		"infrastructure", "action", "auth", "event", "cross-cutting",
		"deployment", "ci", "final",
	}
	for _, p := range allowed {
		if _, ok := initialDispatchByPhase[p]; !ok {
			t.Errorf("phase %q is allowed by goal-create but missing from initialDispatchByPhase matrix", p)
		}
	}
}

// TestResolveDispatchKind covers the full first-dispatch decision: phase matrix,
// the generation-bounce override, and the settings override — in precedence order.
func TestResolveDispatchKind(t *testing.T) {
	override := map[string]DispatchKind{
		PhaseGate:   DispatchPlan,      // force gate back to planning
		PhaseAction: DispatchImplement, // make action skip planning
	}
	tests := []struct {
		name     string
		goal     *Goal
		override map[string]DispatchKind
		want     DispatchKind
	}{
		{
			name: "gate goal uses the dedicated gate executor (matrix default, no override)",
			goal: &Goal{ID: "goal-001", Phase: PhaseGate},
			want: DispatchGate,
		},
		{
			name: "domain goal plans (matrix default)",
			goal: &Goal{ID: "goal-003", Phase: PhaseDomain},
			want: DispatchPlan,
		},
		{
			name:     "settings override flips gate back to plan",
			goal:     &Goal{ID: "goal-001", Phase: PhaseGate},
			override: override,
			want:     DispatchPlan,
		},
		{
			name:     "settings override makes action skip planning",
			goal:     &Goal{ID: "goal-007", Phase: PhaseAction},
			override: override,
			want:     DispatchImplement,
		},
		{
			name:     "unoverridden phase still uses the matrix default",
			goal:     &Goal{ID: "goal-003", Phase: PhaseDomain},
			override: override,
			want:     DispatchPlan,
		},
		{
			name: "generation bounce forces plan even for a gate goal",
			goal: &Goal{ID: "goal-001", Phase: PhaseGate, NextDispatch: dispatchGeneration},
			want: DispatchPlan,
		},
		{
			name:     "generation bounce beats a settings override too",
			goal:     &Goal{ID: "goal-007", Phase: PhaseAction, NextDispatch: dispatchGeneration},
			override: override, // action→implement, but bounce wins
			want:     DispatchPlan,
		},
		{
			name: "implementer marker does NOT override the phase matrix (gate still uses gate executor)",
			goal: &Goal{ID: "goal-001", Phase: PhaseGate, NextDispatch: dispatchImplementer},
			want: DispatchGate,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveDispatchKind(tt.goal, tt.override); got != tt.want {
				t.Fatalf("resolveDispatchKind(%+v) = %s, want %s", tt.goal, got, tt.want)
			}
		})
	}
}

// TestParseDispatchKindName covers the setting.yaml override-value parser.
func TestParseDispatchKindName(t *testing.T) {
	tests := []struct {
		name   string
		want   DispatchKind
		wantOK bool
	}{
		{"plan", DispatchPlan, true},
		{"implement", DispatchImplement, true},
		{"supervisor", DispatchImplement, true},
		{"elaborate", 0, false}, // lifecycle kind, not a phase override
		{"investigate", 0, false},
		{"", 0, false},
		{"garbage", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseDispatchKindName(tt.name)
		if ok != tt.wantOK || (ok && got != tt.want) {
			t.Errorf("parseDispatchKindName(%q) = (%s, %v), want (%s, %v)", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
}

// TestParseDispatchOverrides covers the setting.yaml → phase-override map parse,
// including fail-soft handling of bad rows.
func TestParseDispatchOverrides(t *testing.T) {
	t.Run("nil/empty input returns nil", func(t *testing.T) {
		if got := parseDispatchOverrides(nil); got != nil {
			t.Errorf("parseDispatchOverrides(nil) = %v, want nil", got)
		}
		if got := parseDispatchOverrides(map[string]string{}); got != nil {
			t.Errorf("parseDispatchOverrides(empty) = %v, want nil", got)
		}
	})

	t.Run("valid rows parse", func(t *testing.T) {
		got := parseDispatchOverrides(map[string]string{
			"gate":   "plan",
			"action": "implement",
		})
		want := map[string]DispatchKind{PhaseGate: DispatchPlan, PhaseAction: DispatchImplement}
		if len(got) != len(want) {
			t.Fatalf("got %d rows, want %d (%v)", len(got), len(want), got)
		}
		for p, k := range want {
			if got[p] != k {
				t.Errorf("override[%q] = %s, want %s", p, got[p], k)
			}
		}
	})

	t.Run("bad rows are dropped, good rows survive", func(t *testing.T) {
		got := parseDispatchOverrides(map[string]string{
			"gate":        "implement", // valid
			"not-a-phase": "plan",      // unknown phase → dropped
			"domain":      "garbage",   // invalid kind → dropped
			"action":      "elaborate", // not a phase-selectable kind → dropped
		})
		if len(got) != 1 {
			t.Fatalf("got %v, want only the gate row", got)
		}
		if got[PhaseGate] != DispatchImplement {
			t.Errorf("gate override = %s, want implement", got[PhaseGate])
		}
	})

	t.Run("all-invalid returns nil (clean matrix fallback)", func(t *testing.T) {
		if got := parseDispatchOverrides(map[string]string{"bogus": "nope"}); got != nil {
			t.Errorf("parseDispatchOverrides(all-invalid) = %v, want nil", got)
		}
	})
}

// TestDispatchCommandUnknownKindPanics guards the exhaustiveness contract: an
// out-of-range kind is a programming error, never a silently-empty command that
// would send garbage to a worker window.
func TestDispatchCommandUnknownKindPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("dispatchCommand with unknown kind did not panic")
		}
	}()
	_ = dispatchCommand(DispatchKind(99), DispatchArgs{GoalID: "goal-001"})
}
