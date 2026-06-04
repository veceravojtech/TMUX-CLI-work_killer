package taskvisor

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseCounterLine isolates the "COUNTERS ..." payload from a captured log line
// (which is prefixed by the stdlib log timestamp) and splits it into a key=value
// map. It returns ok=false if the line carries no COUNTERS payload. This mirrors
// the greppable contract B9 relies on: `grep 'COUNTERS ' | <split key=value>`.
func parseCounterLine(line string) (map[string]string, bool) {
	idx := strings.Index(line, "COUNTERS ")
	if idx < 0 {
		return nil, false
	}
	payload := strings.TrimSpace(line[idx+len("COUNTERS "):])
	out := map[string]string{}
	for _, tok := range strings.Fields(payload) {
		kv := strings.SplitN(tok, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[kv[0]] = kv[1]
	}
	return out, true
}

func countCounterLines(out string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "COUNTERS ") {
			n++
		}
	}
	return n
}

const allCounterKeys = "goal cycle phase event retries_code retries_spec retries_val inv_spawned inv_reused cycle_wall_s goal_wall_s"

// daemonWithRuntime builds a Daemon whose per-goal runtime for goalID is seeded
// with the given phase and dispatch clock — the post-goalRuntime-extraction
// equivalent of the old &Daemon{phase:…, currentGoalDispatchTime:…} literal.
func daemonWithRuntime(goalID string, ph phase, dispatch time.Time) *Daemon {
	d := &Daemon{}
	rt := d.runtime(goalID)
	rt.phase = ph
	rt.dispatchTime = dispatch
	return d
}

func TestInstrumentation_EmitsOneLinePerCycle(t *testing.T) {
	d := daemonWithRuntime("goal-020", phaseValidating, time.Now())
	g := &Goal{ID: "goal-020", MaxCodeRetries: 3, CodeRetries: 3, MaxSpecRetries: 1, SpecRetries: 1, MaxValidationRetries: 2, ValidationRetries: 2}

	out := captureLog(t, func() { d.logCounters(g, "fail", 3, 0) })

	require.Equal(t, 1, countCounterLines(out), "exactly one COUNTERS line expected")
	m, ok := parseCounterLine(out)
	require.True(t, ok)
	assert.Equal(t, "goal-020", m["goal"])
	assert.Equal(t, "1", m["cycle"])
	assert.Equal(t, "validating", m["phase"])
	assert.Equal(t, "fail", m["event"])
	assert.Equal(t, "3", m["inv_spawned"])
	assert.Equal(t, "0", m["inv_reused"])
	assert.Contains(t, m, "retries_code")
	assert.Contains(t, m, "cycle_wall_s")
	assert.Contains(t, m, "goal_wall_s")
}

func TestInstrumentation_CountsInvSpawnedNotConfigured(t *testing.T) {
	fs := []ValidationFinding{
		{Rule: "a", ReusedFromCycle: 1},
		{Rule: "b", ReusedFromCycle: 2},
		{Rule: "c", ReusedFromCycle: 0}, // freshly spawned this cycle
	}
	spawned, reused := countInvFindings(fs)
	assert.Equal(t, 1, spawned, "only the non-reused finding counts as spawned")
	assert.Equal(t, 2, reused, "two findings carried reuse markers")
}

func TestInstrumentation_LogLineParses(t *testing.T) {
	line := formatCounterLine("goal-020", 2, "validating", "fail", 2, 1, 0, 3, 1, 733, 735)
	m := map[string]string{}
	for _, tok := range strings.Fields(strings.TrimPrefix(line, "COUNTERS ")) {
		kv := strings.SplitN(tok, "=", 2)
		require.Len(t, kv, 2, "every token must be key=value: %q", tok)
		m[kv[0]] = kv[1]
	}
	require.True(t, strings.HasPrefix(line, "COUNTERS "), "line must carry the COUNTERS prefix")
	assert.Equal(t, map[string]string{
		"goal":         "goal-020",
		"cycle":        "2",
		"phase":        "validating",
		"event":        "fail",
		"retries_code": "2",
		"retries_spec": "1",
		"retries_val":  "0",
		"inv_spawned":  "3",
		"inv_reused":   "1",
		"cycle_wall_s": "733",
		"goal_wall_s":  "735",
	}, m)
}

func TestInstrumentation_NoSchedulingBehaviorChange(t *testing.T) {
	// The counter is side-effect-only: emitting it must NOT mutate goal state,
	// daemon phase, or the cycle clock. This is the unit-level guarantee that
	// wiring logCounters into dispatch/checkValidatingPhase changes nothing about
	// scheduling — it only writes a log line.
	dispatchTime := time.Now().Add(-10 * time.Second)
	d := &Daemon{}
	rt := d.runtime("goal-020")
	rt.phase = phaseSupervising
	rt.dispatchTime = dispatchTime
	g := &Goal{ID: "goal-020", MaxCodeRetries: 3, CodeRetries: 3, StartedAt: "2026-06-03T13:00:00Z"}
	before := *g
	beforePhase := rt.phase
	beforeClock := rt.dispatchTime

	captureLog(t, func() { d.logCounters(g, "dispatch", 0, 0) })

	assert.Equal(t, before, *g, "goal must be unchanged by logCounters")
	assert.Equal(t, beforePhase, d.runtime("goal-020").phase, "daemon phase must be unchanged")
	assert.Equal(t, beforeClock, d.runtime("goal-020").dispatchTime, "cycle clock must be unchanged")
}

func TestInstrumentation_AllKeysPresent(t *testing.T) {
	line := formatCounterLine("g1", 1, "supervising", "dispatch", 0, 0, 0, 0, 0, 0, 0)
	for _, key := range strings.Fields(allCounterKeys) {
		assert.Equal(t, 1, strings.Count(line, key+"="), "key %q must appear exactly once", key)
	}
}

func TestInstrumentation_ConsumedRetriesPerClass(t *testing.T) {
	d := daemonWithRuntime("goal-020", phaseValidating, time.Now())
	g := &Goal{
		ID:                   "goal-020",
		MaxCodeRetries:       3,
		CodeRetries:          1, // consumed 2
		MaxSpecRetries:       2,
		SpecRetries:          1, // consumed 1
		MaxValidationRetries: 2,
		ValidationRetries:    2, // consumed 0
	}
	out := captureLog(t, func() { d.logCounters(g, "fail", 0, 0) })
	m, ok := parseCounterLine(out)
	require.True(t, ok)
	assert.Equal(t, "2", m["retries_code"], "Max-remaining = 3-1")
	assert.Equal(t, "1", m["retries_spec"], "Max-remaining = 2-1")
	assert.Equal(t, "0", m["retries_val"], "Max-remaining = 2-2")
}

func TestInstrumentation_CycleNumberMatchesCurrentCycle(t *testing.T) {
	d := daemonWithRuntime("goal-020", phaseValidating, time.Now())
	g := &Goal{
		ID:             "goal-020",
		MaxCodeRetries: 3,
		CodeRetries:    1, // consumed 2 -> cycle 3
		MaxSpecRetries: 1,
		SpecRetries:    1,
	}
	want := CurrentCycle(g)
	out := captureLog(t, func() { d.logCounters(g, "fail", 0, 0) })
	m, ok := parseCounterLine(out)
	require.True(t, ok)
	assert.Equal(t, "3", m["cycle"])
	assert.Equal(t, want, 3, "sanity: CurrentCycle matches the emitted value")
}

func TestInstrumentation_GoalWallSeconds(t *testing.T) {
	// Both timestamps set -> exact delta.
	g := &Goal{StartedAt: "2026-06-03T13:00:00Z", FinishedAt: "2026-06-03T13:00:30Z"}
	assert.InDelta(t, 30.0, goalWallSeconds(g), 0.001)

	// No StartedAt -> 0.
	g2 := &Goal{}
	assert.Equal(t, 0.0, goalWallSeconds(g2))

	// StartedAt set, no FinishedAt -> now-based, >= 0.
	g3 := &Goal{StartedAt: time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)}
	got := goalWallSeconds(g3)
	assert.GreaterOrEqual(t, got, 0.0)
}

func TestInstrumentation_DispatchLineHasZeroInvCounts(t *testing.T) {
	d := daemonWithRuntime("goal-020", phaseSupervising, time.Now())
	g := &Goal{ID: "goal-020", MaxCodeRetries: 3, CodeRetries: 3}

	for _, event := range []string{"dispatch", "redispatch"} {
		out := captureLog(t, func() { d.logCounters(g, event, 0, 0) })
		m, ok := parseCounterLine(out)
		require.True(t, ok)
		assert.Equal(t, event, m["event"])
		assert.Equal(t, "0", m["inv_spawned"], "investigators unknown pre-validation")
		assert.Equal(t, "0", m["inv_reused"])
	}
}
