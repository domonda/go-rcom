/*
Package rcom provides remote command execution over HTTP with file transfer support.

# Overview

The rcom package allows executing CLI commands on remote servers via HTTP,
with support for transferring input files, receiving output files, and
capturing command output. Each command runs in its own temporary
working directory.

# Basic Usage

Start a server that allows specific commands:

	err := rcom.ListenAndServe(8080, true, "convert", "ffmpeg")

Create a client and execute commands:

	client := rcom.NewClient(
		rcom.ClientWithHost("localhost"),
		rcom.ClientWithPort(8080),
		rcom.ClientWithCmds("convert"),
	)

	result, err := client.Execute(ctx, []string{"-version"}, nil)

Or build a command, where fs.FileReader arguments are sent as input files
and passed as "./" plus their base name:

	result, err := client.Cmd("convert").
		WithArgs(inputFile, "-resize", "50%", "output.png").
		WithResultFilePatterns("output.png").
		Run(ctx)

GET /health on the server responds with 200 OK for health checks.
The golog request ID of the client's context, or a new one,
is sent as X-Request-ID header and used in the server's logs if it is a UUID.

# Command Execution

Commands are represented by the Command struct which includes:
  - Name: The command to execute
  - Args: Command arguments
  - Files: Input files (name -> content)
  - Stdin: Standard input data
  - Env: Environment variables, the server only accepts the AllowedCommandEnvVars
  - ResultFilePatterns: Glob patterns for result files
  - NonErrorExitCodes: Exit codes to not treat as errors

Both local and remote execution are supported:

	// Local execution
	result, callID, err := rcom.ExecuteLocally(ctx, cmd)

	// Remote execution
	result, err := rcom.ExecuteRemotely(ctx, "http://server:8080", cmd)

# File Transfer

Input files are sent with the command and written to a temporary directory.
Result files matching specified patterns are collected and returned:

	cmd := &rcom.Command{
		Name: "convert",
		Args: []string{"input.png", "output.jpg"},
		Files: map[string][]byte{
			"input.png": imageData,
		},
		ResultFilePatterns: []string{"output.jpg"},
	}

	result, err := rcom.ExecuteRemotely(ctx, serverAddr, cmd)
	outputData := result.Files["output.jpg"]

# Security

The server implements several security measures:
  - Command whitelisting: Only explicitly allowed command names can be executed.
    Arguments are passed through unchanged.
  - Path validation: Filenames cannot be ".", ".." or longer than 255 bytes, and cannot
    contain NUL or path separators, also not URL-encoded. All file access goes
    through an os.Root of the working directory.
  - Separate working directory: Each command runs in its own temporary directory,
    accessible only by the user of the process on unix systems, which is not a sandbox
  - Environment: Commands only inherit the InheritedEnvVars,
    and clients can only set the AllowedCommandEnvVars
  - Subprocess cleanup: Child processes are killed on context cancellation
    and after the command exited (not on Windows)
  - Limits: MaxRequestSize, MaxRequestMemory, MaxConnections, StallTimeout,
    MinTransferRate, MaxConcurrentCommands, MaxCommandDuration, MaxOutputSize
    and MaxResultFilesSize.
    Requests are gob-decoded before the command name is checked, the command
    header is limited to 256 KiB and every file is its own message, but gob can
    allocate up to about 14 MiB per connection before the data arrives.
  - Disconnects: A command is canceled when its client closes the connection

The server has no authentication, run it in a private network.

# Executor Interface

The Executor interface provides a common abstraction for both local and remote execution:

	type Executor interface {
		Execute(context.Context, *Command) (*Result, error)
	}

This allows swapping between local and remote execution transparently:

	var exec rcom.Executor
	exec = rcom.LocalExecutor()                     // Local
	exec = rcom.RemoteExecutor("http://server:8080") // Remote

# Result Handling

The Result struct contains:
  - CallID: Unique identifier for the execution
  - ExitCode: Command exit code
  - Output: Combined stdout and stderr
  - Stdout: Standard output
  - Stderr: Standard error
  - Files: Result files (name -> content)

Access result files:

	for filename, data := range result.Files {
		// Process each result file
	}

Or write directly to a file:

	err := result.WriteTo(fs.File("output.png"))

# Graceful Shutdown

The server supports graceful shutdown when configured:

	rcom.GracefulShutdownTimeout = 2 * time.Minute
	err := rcom.ListenAndServe(8080, true, "convert")

On receiving SIGTERM, SIGINT, or SIGHUP, the server will:
 1. Stop accepting new requests, reject requests waiting for memory or a command slot with 503
 2. Wait for in-flight requests to complete, then ListenAndServe returns http.ErrServerClosed
 3. Cancel requests still running after GracefulShutdownTimeout or a second signal,
    which get 503

A zero or negative GracefulShutdownTimeout waits for the running requests
until a second signal.

# Logging

The package uses github.com/domonda/golog for logging. Set a custom logger,
for example with a prefix using github.com/domonda/golog/log:

	rcom.SetLogger(log.WithPrefix("my-rcom-server: "))

Pass nil to disable logging:

	rcom.SetLogger(nil)

# Context Support

All execution functions support context.Context for cancellation and timeouts:

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, callID, err := rcom.ExecuteLocally(ctx, cmd)

Commands respect context cancellation and will terminate running processes.

# Error Handling

Errors are returned with context. Common scenarios:
  - Non-zero exit codes are treated as errors unless in NonErrorExitCodes
  - Command not allowed on server
  - Invalid filenames
  - Context cancellation
  - Timeout expiration
  - Exceeded server limits

# Best Practices

1. Always whitelist only necessary commands on the server
2. Use timeouts on clients to prevent hanging
3. Validate result files before processing
4. Use result patterns to avoid transferring unnecessary files
5. Handle non-zero exit codes explicitly
6. Use context for cancellation support
7. Log call IDs for debugging
*/
package rcom
