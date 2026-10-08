// Package exec runs external commands. Everything goes through Runner so
// tests can replace it, and every command is an argument list, never a shell
// string.
package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	osexec "os/exec"
)

// Spec describes one command.
type Spec struct {
	Dir   string    // working directory; "" = current
	Name  string    // program name or absolute path
	Args  []string  // arguments, passed as-is
	Env   []string  // extra KEY=VALUE pairs, added to the current environment
	Stdin io.Reader // optional

	// Stdout and Stderr, when set, receive the output as it's written
	// (Result then has no Stdout/Stderr). Pass os.Stdout/os.Stderr together
	// with os.Stdin to hand the terminal to the command.
	Stdout io.Writer
	Stderr io.Writer
}

// Result is what a finished command produced. A non-zero exit is a Result
// with Code != 0, not an error.
type Result struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// Runner runs commands. The context carries the timeout.
type Runner interface {
	Run(ctx context.Context, s Spec) (Result, error)
}

// OSRunner runs real processes.
type OSRunner struct{}

// Run implements Runner.
func (OSRunner) Run(ctx context.Context, s Spec) (Result, error) {
	cmd := osexec.CommandContext(ctx, s.Name, s.Args...)
	cmd.Dir = s.Dir
	if len(s.Env) > 0 {
		cmd.Env = append(cmd.Environ(), s.Env...)
	}
	cmd.Stdin = s.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if s.Stdout != nil {
		cmd.Stdout = s.Stdout
	}
	if s.Stderr != nil {
		cmd.Stderr = s.Stderr
	}

	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) && ctx.Err() == nil {
		res.Code = exitErr.ExitCode()
		return res, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, err
	}
	return res, nil
}
