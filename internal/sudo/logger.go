package sudo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp  string `json:"ts"`
	Command    string `json:"cmd"`
	ExitCode   int    `json:"exit"`
	DurationMs int64  `json:"dur_ms"`
	StdoutLen  int    `json:"stdout_len"`
	StderrLen  int    `json:"stderr_len"`
	Error      string `json:"err,omitempty"`
}

var logMutex sync.Mutex

func LogExecution(workingDir string, entry LogEntry) {
	logMutex.Lock()
	defer logMutex.Unlock()

	entry.Timestamp = time.Now().Format(time.RFC3339)

	logDir := filepath.Join(workingDir, ".tmux-cli", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}

	logFile := filepath.Join(logDir, "sudo.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f.Write(append(data, '\n'))
}
