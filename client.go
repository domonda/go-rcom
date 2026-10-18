package rcom

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ungerik/go-fs"
)

// Client executes a fixed set of allowed commands on an rcom server.
type Client struct {
	cmds    map[string]bool
	host    string
	port    uint16
	timeout time.Duration
}

// ExecuteWithCommand executes the allowed cmd on the server with the given
// arguments and input files and returns the result. Each input file is sent
// under its base name; two input files with the same base name are an error.
func (c *Client) ExecuteWithCommand(ctx context.Context, cmd string, cmdArgs []string, files []fs.FileReader, resultFilePatterns ...string) (*Result, error) {
	if !c.cmds[cmd] {
		return nil, fmt.Errorf("rcom.Client: command %q not allowed", cmd)
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
			// Send the file under its base name, also when the name
			// is a Windows path like the file name of a browser upload
			name := file.Name()
			if i := strings.LastIndexAny(name, fileNameSeparators); i >= 0 {
				name = name[i+1:]
			}
			if _, exists := command.Files[name]; exists {
				return nil, fmt.Errorf("rcom.Client: more than one input file named %q", name)
			}
			command.Files[name] = data
		}
	}

	if c.timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		ctx = timeoutCtx
	}

	return ExecuteRemotely(ctx, c.addr(), command)
}

// addr returns the server URL, with brackets around an IPv6 host
func (c *Client) addr() string {
	return "http://" + net.JoinHostPort(c.host, strconv.Itoa(int(c.port)))
}

// Execute executes the client's only allowed command on the server
// with the given arguments and input files and returns the result.
// It panics if the client does not allow exactly one command,
// use ExecuteWithCommand for clients with several commands.
func (c *Client) Execute(ctx context.Context, cmdArgs []string, files []fs.FileReader, resultFilePatterns ...string) (*Result, error) {
	if len(c.cmds) != 1 {
		panic(fmt.Errorf("rcom.Client.Execute needs exactly one allowed command, the client has %d", len(c.cmds)))
	}
	for cmd := range c.cmds {
		return c.ExecuteWithCommand(ctx, cmd, cmdArgs, files, resultFilePatterns...)
	}
	panic("unreachable statement")
}

// ClientOption represents Client option.
type ClientOption func(*Client)

// ClientWithCmds adds commands the client is allowed to execute.
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

// ClientWithHost sets the host name or IP address of the server.
func ClientWithHost(host string) ClientOption {
	return func(c *Client) { c.host = host }
}

// ClientWithPort sets the port of the server.
func ClientWithPort(port uint16) ClientOption {
	return func(c *Client) { c.port = port }
}

// ClientWithTimeOut sets a timeout for every executed command,
// zero means no timeout.
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
