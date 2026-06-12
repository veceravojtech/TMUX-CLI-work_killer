package sudo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHelperProcess is the fake subprocess that simulates sudo behavior.
// It is invoked by the test binary itself when GO_TEST_HELPER_PROCESS=1.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	// Find the "--" separator that marks the start of the simulated command args
	idx := 0
	for i, a := range args {
		if a == "--" {
			idx = i + 1
			break
		}
	}
	if idx == 0 || idx >= len(args) {
		fmt.Fprintf(os.Stderr, "helper: no args after --\n")
		os.Exit(1)
	}

	scenario := os.Getenv("GO_TEST_SCENARIO")

	switch scenario {
	case "success":
		fmt.Fprint(os.Stdout, "hello\n")
		os.Exit(0)

	case "stderr":
		fmt.Fprint(os.Stdout, "out\n")
		fmt.Fprint(os.Stderr, "err\n")
		os.Exit(0)

	case "nonzero":
		fmt.Fprint(os.Stderr, "ls: cannot access '/nonexistent': No such file or directory\n")
		os.Exit(2)

	case "password_echo":
		// Read stdin (the password) and echo it to stdout
		buf := make([]byte, 4096)
		n, _ := os.Stdin.Read(buf)
		password := strings.TrimRight(string(buf[:n]), "\n")
		fmt.Fprintf(os.Stdout, "password=%s\n", password)
		os.Exit(0)

	case "sleep":
		// Sleep for a long time — will be killed by context cancellation
		time.Sleep(30 * time.Second)
		os.Exit(0)

	case "special_chars":
		// Simulate bash -c handling: just echo the command args back
		// The args after -- are: sudo -S bash -c <command>
		// We want to show the command was passed correctly
		// args[idx:] = ["sudo", "-S", "bash", "-c", "<command>"]
		if len(args) > idx+4 {
			fmt.Fprint(os.Stdout, args[idx+4]+"\n")
		}
		os.Exit(0)

	case "duration":
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(os.Stdout, "done\n")
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "unknown scenario: %s\n", scenario)
		os.Exit(1)
	}
}

// fakeExecCommand returns an exec.CommandContext replacement that re-invokes
// the test binary as a helper process with the given scenario.
func fakeExecCommand(scenario string) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_TEST_HELPER_PROCESS=1",
			"GO_TEST_SCENARIO="+scenario,
		)
		return cmd
	}
}

func TestNewExecutor(t *testing.T) {
	e := NewExecutor("mypass", 45*time.Second)
	require.NotNil(t, e)
	assert.Equal(t, "mypass", e.password)
	assert.Equal(t, 45*time.Second, e.defaultTimeout)
}

func TestExecutor_ExecuteStream_Success(t *testing.T) {
	origExecCommand := execCommand
	execCommand = fakeExecCommand("stderr")
	t.Cleanup(func() { execCommand = origExecCommand })

	e := NewExecutor("testpass", 30*time.Second)
	var stdout, stderr strings.Builder
	err := e.ExecuteStream(context.Background(), "echo out; echo err >&2", &stdout, &stderr)

	require.NoError(t, err)
	assert.Equal(t, "out\n", stdout.String())
	assert.Equal(t, "err\n", stderr.String())
}

func TestExecutor_ExecuteStream_Timeout(t *testing.T) {
	origExecCommand := execCommand
	execCommand = fakeExecCommand("sleep")
	t.Cleanup(func() { execCommand = origExecCommand })

	e := NewExecutor("testpass", 100*time.Millisecond)
	var stdout, stderr strings.Builder
	start := time.Now()
	err := e.ExecuteStream(context.Background(), "sleep 300", &stdout, &stderr)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 2*time.Second)
}

func TestExecutor_ExecuteStream_EmptyCommand(t *testing.T) {
	e := NewExecutor("testpass", 30*time.Second)
	err := e.ExecuteStream(context.Background(), "", os.Stdout, os.Stderr)
	require.ErrorIs(t, err, ErrEmptyCommand)
}

func TestExecutor_ExecuteStream_NoPassword(t *testing.T) {
	e := NewExecutor("", 30*time.Second)
	err := e.ExecuteStream(context.Background(), "whoami", os.Stdout, os.Stderr)
	require.ErrorIs(t, err, ErrNoPassword)
}

func TestExecutor_ExecuteStream_NonZeroExit(t *testing.T) {
	origExecCommand := execCommand
	execCommand = fakeExecCommand("nonzero")
	t.Cleanup(func() { execCommand = origExecCommand })

	e := NewExecutor("testpass", 30*time.Second)
	var stdout, stderr strings.Builder
	err := e.ExecuteStream(context.Background(), "ls /nonexistent", &stdout, &stderr)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 2")
	assert.Contains(t, stderr.String(), "No such file or directory")
}

func TestExecutor_ExecuteStream_ContextTimeout(t *testing.T) {
	origExecCommand := execCommand
	execCommand = fakeExecCommand("sleep")
	t.Cleanup(func() { execCommand = origExecCommand })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	e := NewExecutor("testpass", 30*time.Second)
	var stdout, stderr strings.Builder
	err := e.ExecuteStream(ctx, "sleep 300", &stdout, &stderr)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecutor_ExecuteStream_ContextCanceled(t *testing.T) {
	origExecCommand := execCommand
	execCommand = fakeExecCommand("sleep")
	t.Cleanup(func() { execCommand = origExecCommand })

	ctx, cancel := context.WithCancel(context.Background())

	e := NewExecutor("testpass", 30*time.Second)

	// Cancel after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	var stdout, stderr strings.Builder
	err := e.ExecuteStream(ctx, "sleep 300", &stdout, &stderr)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestExecutor_ExecuteStream_PasswordPipedToStdin(t *testing.T) {
	origExecCommand := execCommand
	execCommand = fakeExecCommand("password_echo")
	t.Cleanup(func() { execCommand = origExecCommand })

	e := NewExecutor("s3cret!P@ss", 30*time.Second)
	var stdout, stderr strings.Builder
	err := e.ExecuteStream(context.Background(), "read_stdin", &stdout, &stderr)

	require.NoError(t, err)
	assert.Equal(t, "password=s3cret!P@ss\n", stdout.String())
}

func TestExecutor_ExecuteStream_SpecialCharsInCommand(t *testing.T) {
	origExecCommand := execCommand
	execCommand = fakeExecCommand("special_chars")
	t.Cleanup(func() { execCommand = origExecCommand })

	e := NewExecutor("testpass", 30*time.Second)
	cmd := "echo 'hello world' && cat /etc/hosts"
	var stdout, stderr strings.Builder
	err := e.ExecuteStream(context.Background(), cmd, &stdout, &stderr)

	require.NoError(t, err)
	assert.Equal(t, cmd+"\n", stdout.String())
}

// TestExecutor_ExecuteStream_SudoNotFound verifies ErrSudoNotFound when the binary is missing.
func TestExecutor_ExecuteStream_SudoNotFound(t *testing.T) {
	origExecCommand := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Use a name without path separators so LookPath runs and fails with ErrNotFound
		return exec.CommandContext(ctx, "nonexistent-sudo-binary-"+strconv.Itoa(os.Getpid()))
	}
	t.Cleanup(func() { execCommand = origExecCommand })

	e := NewExecutor("testpass", 30*time.Second)
	var stdout, stderr strings.Builder
	err := e.ExecuteStream(context.Background(), "whoami", &stdout, &stderr)

	require.ErrorIs(t, err, ErrSudoNotFound)
}
