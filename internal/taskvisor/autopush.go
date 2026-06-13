package taskvisor

import (
	"log"
	"strings"
)

// autopush.go — completion-time auto-push (taskvisor.auto_push). After a full
// taskvisor run finishes, the per-goal auto_commit step has already landed every
// resolved goal's changeset as a LOCAL commit, but nothing publishes them. This
// step runs exactly one plain `git push` ONCE per finished run (from inside
// deactivateOnCompletion), publishing the whole run's commits in a single push
// (one CI trigger) instead of N per-goal pushes.
//
// Contract: warn-only and default-OFF (pushing is outward-facing). A push
// failure — including "no configured upstream", which is an ordinary warn-only
// path, NOT a special case — must NEVER alter goal status, burn retries, or
// block teardown: it logs a warning and returns. Plain `git push` only: no
// remote/branch arg, no --force, no -u. Reuses the autoCommitGit runner seam
// verbatim (mirrors autoCommitGoal's invocation idiom, incl. the -C workDir
// prefix so the push runs in the project tree, not the daemon's cwd).
func (d *Daemon) autoPushOnCompletion() {
	if !d.autoPush {
		return
	}
	out, stderr, code, err := d.autoCommitGit("-C", d.workDir, "push")
	if code != 0 || err != nil {
		log.Printf("warning: auto-push: git push failed (exit %d, err %v): %s", code, err, strings.TrimSpace(stderr))
		return
	}
	// No-op honesty: real `git push` reports "Everything up-to-date" on stderr,
	// so scan both streams. An unresolvable upstream is NOT a no-op — it exits
	// non-zero above and routes to the warn-only path, never here.
	if strings.Contains(out+"\n"+stderr, "Everything up-to-date") {
		log.Printf("nothing to push")
	} else {
		log.Printf("auto-pushed run commits to remote")
	}
}
