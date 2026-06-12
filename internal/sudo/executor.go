package sudo

import (
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

// Executor runs commands via sudo, piping the configured password to stdin.
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
	return &preparedCommand{cmd: cmd, ctx: ctx, cancel: cancel}, nil
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
