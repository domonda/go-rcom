/*
Package rcom provides remote command execution over HTTP with file transfer support.

# Overview

The rcom package allows executing CLI commands on remote servers via HTTP,
with support for transferring input files, receiving output files, and
capturing command output. Commands run in isolated temporary directories
for security and cleanliness.

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

# Command Execution

Commands are represented by the Command struct which includes:
- Name: The command to execute
- Args: Command arguments
- Files: Input files (name -> content)
- Stdin: Standard input data
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
- Command whitelisting: Only explicitly allowed commands can be executed
- Path validation: Filenames cannot contain path separators
- Isolated execution: Each command runs in a unique temporary directory
- Subprocess cleanup: Child processes are killed on context cancellation

# Executor Interface

The Executor interface provides a common abstraction for both local and remote execution:

	type Executor interface {
		Execute(context.Context, *Command) (*Result, error)
	}

This allows swapping between local and remote execution transparently:

	var exec rcom.Executor
	exec = rcom.LocalExecutor()         // Local
	exec = remoteClient                  // Remote (via Client)

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
1. Stop accepting new requests
2. Wait for in-flight requests to complete
3. Shutdown after timeout if requests are still running

# Logging

The package uses github.com/domonda/golog for logging. Set a custom logger:

	logger := golog.NewLogger("my-rcom-server")
	rcom.SetLogger(logger)

Pass nil to disable logging:

	rcom.SetLogger(nil)

# Context Support

All execution functions support context.Context for cancellation and timeouts:

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := rcom.ExecuteLocally(ctx, cmd)

Commands respect context cancellation and will terminate running processes.

# Error Handling

Errors are returned with context. Common scenarios:
- Non-zero exit codes are treated as errors unless in NonErrorExitCodes
- Command not allowed on server
- Invalid filenames
- Context cancellation
- Timeout expiration

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
