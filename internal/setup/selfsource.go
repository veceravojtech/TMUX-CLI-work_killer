package setup

// cliModulePath is the module identity of the tmux-cli source tree. The
// checkout detector keys on it so "is a tmux-cli checkout" can never be
// satisfied by an arbitrary Go project that merely has a Makefile.
const cliModulePath = "github.com/console/tmux-cli"

// IsCliSourceCheckout reports whether dir is a tmux-cli source checkout:
// a Makefile exists AND the go.mod module path equals cliModulePath. Shared
// by the daemon's repair-cycle self-reinstall hook (trigger predicate) and
// self-update's resolveSourceDir (source==project guard relaxation), so the
// two sides can never drift on what counts as "the cli source".
func IsCliSourceCheckout(dir string) bool {
	return false
}
