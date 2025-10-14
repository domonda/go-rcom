package rcom

import "context"

// Executor is the interface for executing commands.
// Implementations can execute commands locally or remotely.
type Executor interface {
	// Execute runs a command and returns the result.
	Execute(context.Context, *Command) (*Result, error)
}

// ExecutorFunc is a function adapter that implements the Executor interface.
type ExecutorFunc func(context.Context, *Command) (*Result, error)

// Execute calls the function, implementing the Executor interface.
func (f ExecutorFunc) Execute(ctx context.Context, cmd *Command) (*Result, error) {
	return f(ctx, cmd)
}

// LocalExecutor returns an Executor that executes commands locally.
// Commands run in isolated temporary directories on the local machine.
func LocalExecutor() Executor {
	return ExecutorFunc(func(ctx context.Context, cmd *Command) (*Result, error) {
		result, _, err := ExecuteLocally(ctx, cmd)
		return result, err
	})
}
