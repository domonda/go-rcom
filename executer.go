package rcom

import "context"

type Executer interface {
	Execute(context.Context, *Command) (*Result, error)
}

type ExecuterFunc func(context.Context, *Command) (*Result, error)

func (f ExecuterFunc) Execute(ctx context.Context, cmd *Command) (*Result, error) {
	return f(ctx, cmd)
}

func LocalExecuter() Executer {
	return ExecuterFunc(func(ctx context.Context, cmd *Command) (*Result, error) {
		result, _, err := ExecuteLocally(ctx, cmd)
		return result, err
	})
}
