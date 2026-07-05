package taskvisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"
)

// goalTransitionHookTimeout bounds ONE goal-transition hook subprocess. The hook
// runs in a detached goroutine, so this is a subprocess-leak guard, not a tick
// deadline — the daemon tick is never blocked regardless of the value. 5s is
// generous for the near-instant canonical `notify-orchestrator` body yet short
// enough to reap a wedged command.
const goalTransitionHookTimeout = 5 * time.Second

// goalHookRunner is the injectable seam for firing the goal-transition hook,
// mirroring the ScriptRunnerFunc precedent: the default (defaultGoalHookRunner)
// is asynchronous; tests inject a synchronous fake to assert the env
// deterministically.
type goalHookRunner func(command string, env []string)

// defaultGoalHookRunner runs command fire-and-forget in a goroutine bounded by
// goalTransitionHookTimeout. Errors — including a non-zero exit or a timeout —
// are log-only and never propagate to the daemon tick. `sh -c` (not a bare exec)
// is used so the canonical body's `$GOAL_ID`/`$NEW_STATUS` shell expansion works.
func defaultGoalHookRunner(command string, env []string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), goalTransitionHookTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("goal-transition hook failed: %v (output: %s)", err, string(out))
		}
	}()
}

// fireGoalTransitionHook runs the configured goal_transition hook once, after a
// committed goal status/phase transition, exporting the five transition vars on
// top of the daemon process's os.Environ() (so PATH — for `tmux-cli` — and
// TMUX_CLI_ORCHESTRATOR_PANE reach the hook). It is a no-op when no hook is
// configured (zero value = disabled), keeping behavior byte-identical to a build
// without the hook. PHASE is passed explicitly per call site (the runtime
// lifecycle phase), not read from goal.Phase, so the supervising->validating fire
// (status unchanged) still conveys the phase change.
func (d *Daemon) fireGoalTransitionHook(goalID, oldStatus, newStatus, phase string, cycle int) {
	if d.goalTransitionHook == "" {
		return
	}
	runner := d.hookRunnerFn
	if runner == nil {
		runner = defaultGoalHookRunner
	}
	env := append(os.Environ(),
		"GOAL_ID="+goalID,
		"OLD_STATUS="+oldStatus,
		"NEW_STATUS="+newStatus,
		"PHASE="+phase,
		fmt.Sprintf("CYCLE=%d", cycle),
	)
	runner(d.goalTransitionHook, env)
}
