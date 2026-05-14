package sudo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

var (
	ErrEmptyCommand = errors.New("sudo: command cannot be empty")
	ErrNoPassword   = errors.New("sudo: password not configured")
	ErrSudoNotFound = errors.New("sudo: sudo binary not found")
)

// SudoExecutor defines the interface for executing commands via sudo.
type SudoExecutor interface {
	Execute(ctx context.Context, command string) (*Result, error)
}

// Result captures all output from a sudo command execution.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Executor is the production implementation of SudoExecutor.
type Executor struct {
	password       string
	defaultTimeout time.Duration
}

var execCommand = exec.CommandContext

// NewExecutor creates a new Executor with the given password and default timeout.
func NewExecutor(password string, defaultTimeout time.Duration) *Executor {
	return &Executor{
		password:       password,
		defaultTimeout: defaultTimeout,
	}
}

type preparedCommand struct {
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
	start  time.Time
}

func (e *Executor) prepareCommand(ctx context.Context, command string) (*preparedCommand, error) {
	if command == "" {
		return nil, ErrEmptyCommand
	}
	if e.password == "" {
		return nil, ErrNoPassword
	}

	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && e.defaultTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, e.defaultTimeout)
	}

	cmd := execCommand(ctx, "sudo", "-S", "bash", "-c", command)
	return &preparedCommand{cmd: cmd, ctx: ctx, cancel: cancel, start: time.Now()}, nil
}

func (e *Executor) startAndPipePassword(cmd *exec.Cmd) error {
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("sudo exec failed: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrSudoNotFound
		}
		return fmt.Errorf("sudo exec failed: %w", err)
	}

	stdinPipe.Write([]byte(e.password + "\n"))
	stdinPipe.Close()
	return nil
}

func handleWaitError(err error, ctx context.Context) error {
	if ctx.Err() != nil {
		return fmt.Errorf("sudo command timed out: %w", ctx.Err())
	}
	if errors.Is(err, exec.ErrNotFound) {
		return ErrSudoNotFound
	}
	return err
}

// Execute runs a command via sudo -S bash -c, piping the password to stdin.
func (e *Executor) Execute(ctx context.Context, command string) (*Result, error) {
	pc, err := e.prepareCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	if pc.cancel != nil {
		defer pc.cancel()
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	pc.cmd.Stdout = &stdoutBuf
	pc.cmd.Stderr = &stderrBuf

	if err := e.startAndPipePassword(pc.cmd); err != nil {
		return nil, err
	}

	err = pc.cmd.Wait()
	duration := time.Since(pc.start)

	if err != nil {
		if pc.ctx.Err() != nil {
			return nil, fmt.Errorf("sudo command timed out: %w", pc.ctx.Err())
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &Result{
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
				ExitCode: exitErr.ExitCode(),
				Duration: duration,
			}, nil
		}

		return nil, handleWaitError(err, pc.ctx)
	}

	return &Result{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: 0,
		Duration: duration,
	}, nil
}

// ExecuteStream runs a command via sudo, streaming stdout/stderr to the provided writers.
func (e *Executor) ExecuteStream(ctx context.Context, command string, stdout, stderr io.Writer) error {
	pc, err := e.prepareCommand(ctx, command)
	if err != nil {
		return err
	}
	if pc.cancel != nil {
		defer pc.cancel()
	}

	pc.cmd.Stdout = stdout
	pc.cmd.Stderr = stderr

	if err := e.startAndPipePassword(pc.cmd); err != nil {
		return err
	}

	err = pc.cmd.Wait()
	if err != nil {
		return handleWaitError(err, pc.ctx)
	}
	return nil
}
