package utils

import (
	"errors"

	"github.com/spf13/cobra"
)

// hookError marks an error a command's own hook returned; any other error is Cobra rejecting the command line.
type hookError struct{ err error }

func (e hookError) Error() string { return e.err.Error() }
func (e hookError) Unwrap() error { return e.err }

// MarkHookErrors wraps what every error-returning hook of c and its subcommands returns, so IsHookError can
// tell a command's own failure from Cobra's parse error. restore puts the original hooks back.
func MarkHookErrors(c *cobra.Command) (restore func()) {
	var restores []func()
	for _, hook := range []*func(*cobra.Command, []string) error{
		&c.PersistentPreRunE, &c.PreRunE, &c.RunE, &c.PostRunE, &c.PersistentPostRunE,
	} {
		if run := *hook; run != nil {
			*hook = func(cmd *cobra.Command, args []string) error { return markHookError(run(cmd, args)) }
			restores = append(restores, func() { *hook = run })
		}
	}
	for _, sub := range c.Commands() {
		restores = append(restores, MarkHookErrors(sub))
	}
	return func() {
		for _, r := range restores {
			r()
		}
	}
}

func markHookError(err error) error {
	if err == nil {
		return nil
	}
	return hookError{err: err}
}

// IsHookError reports whether err came from a hook MarkHookErrors wrapped.
func IsHookError(err error) bool { return errors.As(err, new(hookError)) }
