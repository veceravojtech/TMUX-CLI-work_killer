package setup

import (
	"os"
	"path/filepath"
	"strings"
)

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
	fi, err := os.Stat(filepath.Join(dir, "Makefile"))
	if err != nil || fi.IsDir() {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return goModModulePath(data) == cliModulePath
}

// goModModulePath extracts the module path from go.mod content, "" when no
// module directive is found.
func goModModulePath(gomod []byte) string {
	for _, line := range strings.Split(string(gomod), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}
