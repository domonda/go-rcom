package rcom

import (
	"context"
	"fmt"
	"time"

	"github.com/ungerik/go-fs"
)

// Client is a base for building rcom base clients.
type Client struct {
	cmds    map[string]bool
	host    string
	port    uint16
	timeout time.Duration
}

// Execute executes the services' cmd remotely with given arguments and returns a result.
func (c *Client) ExecuteWithCommand(ctx context.Context, cmd string, cmdArgs []string, files []fs.FileReader, resultFilePatterns ...string) (*Result, error) {
	if !c.cmds[cmd] {
		return nil, fmt.Errorf("command %q not allowd", cmd)
	}

	command := &Command{
		Name:               cmd,
		Args:               cmdArgs,
		ResultFilePatterns: resultFilePatterns,
	}

	if len(files) > 0 {
		command.Files = make(map[string][]byte)

		for _, file := range files {
			data, err := file.ReadAllContext(ctx)
			if err != nil {
				return nil, err
			}
			command.Files[file.Name()] = data
		}
	}

	if c.timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		ctx = timeoutCtx
	}

	return ExecuteRemotely(ctx, fmt.Sprintf("http://%s:%d", c.host, c.port), command)
}

// Execute executes the service remotely with given arguments and returns a result.
// Method will panic if it is called when client allows execution of more then one
// commands, in that the ExecuteWithCommand should be used.
func (c *Client) Execute(ctx context.Context, cmdArgs []string, files []fs.FileReader, resultFilePatterns ...string) (*Result, error) {
	if len(c.cmds) != 1 {
		panic(fmt.Errorf("client allows execution of multiple commands"))
	}
	for cmd := range c.cmds {
		return c.ExecuteWithCommand(ctx, cmd, cmdArgs, files, resultFilePatterns...)
	}
	panic("unreachable statement")
}

// ClientOption represents Client option.
type ClientOption func(*Client)

// WithHost sets Client.cmds attribute.
func ClientWithCmds(cmds ...string) ClientOption {
	return func(c *Client) {
		if c.cmds == nil {
			c.cmds = make(map[string]bool)
		}
		for _, cmd := range cmds {
			c.cmds[cmd] = true
		}
	}
}

// WithHost sets Client.host attribute.
func ClientWithHost(host string) ClientOption {
	return func(c *Client) { c.host = host }
}

// WithPort sets Client.port attribute.
func ClientWithPort(port uint16) ClientOption {
	return func(c *Client) { c.port = port }
}

// WithPort sets Client.timeout attribute.
func ClientWithTimeOut(timeout time.Duration) ClientOption {
	return func(c *Client) { c.timeout = timeout }
}

// NewClient returns new client with attributes set by given opts.
func NewClient(opts ...ClientOption) *Client {
	c := new(Client)
	for _, opt := range opts {
		opt(c)
	}
	return c
}
