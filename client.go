package rcom

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	retries int
}

// ExecuteWithCommand executes the allowed cmd on the server with the given
// arguments and input files and returns the result. Each input file is sent
// under its base name; two input files with the same base name are an error.
func (c *Client) ExecuteWithCommand(ctx context.Context, cmd string, cmdArgs []string, files []fs.FileReader, resultFilePatterns ...string) (*Result, error) {
	inputFiles, err := readInputFiles(ctx, files)
	if err != nil {
		return nil, err
	}
	return c.run(ctx, &Command{
		Name:               cmd,
		Args:               cmdArgs,
		Files:              inputFiles,
		ResultFilePatterns: resultFilePatterns,
	})
}

// run executes the command on the server with the timeout and retries of the client
func (c *Client) run(ctx context.Context, command *Command) (*Result, error) {
	if !c.cmds[command.Name] {
		return nil, fmt.Errorf("rcom.Client: command %q not allowed", command.Name)
	}
	err := command.Validate()
	if err != nil {
		return nil, err
	}

	if c.timeout > 0 {
		timeoutCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, fmt.Errorf("rcom command %q timed out after %s: %w", command.Name, c.timeout, context.DeadlineExceeded))
		defer cancel()
		ctx = timeoutCtx
	}

	for attempt := 1; ; attempt++ {
		result, err := ExecuteRemotely(ctx, c.addr(), command)
		if err == nil || attempt > c.retries || !isRetryable(ctx, err) {
			return result, err
		}
		log.DebugCtx(ctx, "Retrying rcom command").
			Err(err).
			Str("command", command.Name).
			Int("attempt", attempt).
			Log()
		select {
		case <-time.After(retryBaseDelay * time.Duration(fibonacci(attempt))):
		case <-ctx.Done():
			return nil, err
		}
	}
}

// retryBaseDelay is the delay before the first retry
var retryBaseDelay = time.Second

// isRetryable returns if a request that failed with err should be retried:
// if it failed before reaching the server, or with a status
// that load balancers respond when the server is not available
func isRetryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if responseErr, ok := errors.AsType[*ResponseError](err); ok {
		switch responseErr.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	return true
}

// fibonacci returns the n-th Fibonacci number, starting with 1, 1, 2
func fibonacci(n int) int {
	a, b := 0, 1
	for range n {
		a, b = b, a+b
	}
	return a
}

// readInputFiles reads the files into a map of their base names,
// also when the name is a Windows path like the file name of a browser upload
func readInputFiles(ctx context.Context, files []fs.FileReader) (map[string][]byte, error) {
	if len(files) == 0 {
		return nil, nil
	}
	inputFiles := make(map[string][]byte, len(files))
	for _, file := range files {
		data, err := file.ReadAllContext(ctx)
		if err != nil {
			return nil, err
		}
		name := baseName(file.Name())
		if _, exists := inputFiles[name]; exists {
			return nil, fmt.Errorf("rcom.Client: more than one input file named %q", name)
		}
		inputFiles[name] = data
	}
	return inputFiles, nil
}

// baseName returns the name without a directory part
// separated by / or \
func baseName(name string) string {
	if i := strings.LastIndexAny(name, fileNameSeparators); i >= 0 {
		return name[i+1:]
	}
	return name
}

// addr returns the server URL, with brackets around an IPv6 host
// also if the host was configured with them
func (c *Client) addr() string {
	host := strings.TrimSuffix(strings.TrimPrefix(c.host, "["), "]")
	return "http://" + net.JoinHostPort(host, strconv.Itoa(int(c.port)))
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

// ClientWithTimeout sets a timeout for every executed command,
// including its retries, zero means no timeout.
func ClientWithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) { c.timeout = timeout }
}

// ClientWithTimeOut sets a timeout for every executed command.
//
// Deprecated: Use ClientWithTimeout.
func ClientWithTimeOut(timeout time.Duration) ClientOption {
	return ClientWithTimeout(timeout)
}

// ClientWithRetries retries a command up to retries times
// if the request failed before reaching the server, or the
// server responded with 502, 503 or 504 as load balancers do
// while a server is not available. A failed command is not retried.
// The delays between the attempts follow the Fibonacci
// sequence in seconds: 1, 1, 2, 3, 5 and so on.
// Retried commands can run more than once if a request failed
// after reaching the server, so they should be idempotent.
func ClientWithRetries(retries int) ClientOption {
	return func(c *Client) { c.retries = retries }
}

// NewClient returns new client with attributes set by given opts.
func NewClient(opts ...ClientOption) *Client {
	c := new(Client)
	for _, opt := range opts {
		opt(c)
	}
	return c
}
