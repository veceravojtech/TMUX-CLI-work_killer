package setup

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var executablePath = resolvedExecutable

func resolvedExecutable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		log.Printf("warning: EvalSymlinks failed for %s: %v — using unresolved path", p, err)
		return p, nil
	}
	return resolved, nil
}

type buildStamp struct {
	path  string
	mtime time.Time
	size  int64
}

var (
	stamp     *buildStamp
	stampOnce sync.Once
)

func initBuildStamp() {
	stampOnce.Do(func() {
		p, err := executablePath()
		if err != nil {
			log.Printf("warning: cannot resolve executable path: %v — stale-binary detection disabled", err)
			return
		}
		fi, err := os.Stat(p)
		if err != nil {
			log.Printf("warning: cannot stat executable %s: %v — stale-binary detection disabled", p, err)
			return
		}
		stamp = &buildStamp{
			path:  p,
			mtime: fi.ModTime(),
			size:  fi.Size(),
		}
	})
}

func resetBuildStamp() {
	stamp = nil
	stampOnce = sync.Once{}
}

func ResetBuildStampForTest()                            { resetBuildStamp() }
func InitBuildStampForTest()                             { initBuildStamp() }
func SetExecutablePathForTest(fn func() (string, error)) { executablePath = fn }
func RestoreExecutablePathForTest()                      { executablePath = resolvedExecutable }

func BinaryStale() (bool, string) {
	if stamp == nil {
		return false, ""
	}
	fi, err := os.Stat(stamp.path)
	if err != nil {
		return true, fmt.Sprintf("binary stat failed: %v", err)
	}
	if fi.ModTime() != stamp.mtime || fi.Size() != stamp.size {
		return true, fmt.Sprintf("binary replaced: mtime %s → %s, size %d → %d",
			stamp.mtime.Format(time.RFC3339), fi.ModTime().Format(time.RFC3339),
			stamp.size, fi.Size())
	}
	return false, ""
}

func init() {
	initBuildStamp()
}
