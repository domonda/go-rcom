# go-rcom

Go package for executing remote CLI commands with input/output file support over HTTP.

## Features

- **Remote Command Execution**: Execute CLI commands on remote servers via HTTP
- **File Transfer**: Send input files and receive output files with commands
- **Result Patterns**: Glob patterns to select which result files to return
- **Stdin Support**: Provide standard input data to commands
- **Local Execution**: Execute commands locally with the same interface
- **Client/Server Architecture**: HTTP-based communication with gob encoding
- **Graceful Shutdown**: Server supports graceful shutdown on signals
- **Command Whitelisting**: Server restricts execution to explicitly allowed commands
- **Timeout Support**: Configurable timeouts for command execution
- **Separate Working Directories**: Each command runs in its own temporary directory
- **Server Limits**: Request size, connections, stalled transfers, concurrent commands,
  command duration, output and result file sizes
- **Context Support**: Full context.Context support for cancellation

## Installation

Requires Go 1.26 or later.

```bash
go get github.com/domonda/go-rcom
```

Clients and servers must use the same wire format: v0.1.0 changed it,
so it can't talk to earlier untagged builds.
See [CHANGELOG.md](CHANGELOG.md) for the changes of each release.

## Quick Start

### Server

Start a server that allows execution of specific commands:

```go
package main

import (
    "log"
    "github.com/domonda/go-rcom"
)

func main() {
    // Allow execution of 'convert' and 'ffmpeg' commands
    err := rcom.ListenAndServe(8080, true, "convert", "ffmpeg")
    if err != nil {
        log.Fatal(err)
    }
}
```

The server will:
- Listen on port 8080
- Enable graceful shutdown on SIGTERM/SIGINT/SIGHUP
- Only execute whitelisted commands

### Client

Execute commands remotely:

```go
package main

import (
    "context"
    "log"
    "time"
    "github.com/domonda/go-rcom"
    "github.com/ungerik/go-fs"
)

func main() {
    ctx := context.Background()

    // Create client
    client := rcom.NewClient(
        rcom.ClientWithHost("localhost"),
        rcom.ClientWithPort(8080),
        rcom.ClientWithCmds("convert"),
        rcom.ClientWithTimeOut(30 * time.Second),
    )

    // Prepare input files
    inputFile := fs.File("/path/to/input.png")

    // Execute command with input file and collect result files
    result, err := client.Execute(
        ctx,
        []string{"-resize", "50%", "input.png", "output.png"}, // command args
        []fs.FileReader{inputFile},                             // input files
        "output.png",                                            // result file patterns
    )
    if err != nil {
        log.Fatal(err)
    }

    // Save result file
    err = result.WriteTo(fs.File("/path/to/output.png"))
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Command output: %s", result.Output)
    log.Printf("Exit code: %d", result.ExitCode)
}
```

## Usage

### Command Structure

Commands are defined using the `Command` struct:

```go
cmd := &rcom.Command{
    Name: "convert",                          // Command to execute
    Args: []string{"-resize", "50%", "in.png", "out.png"}, // Arguments
    Files: map[string][]byte{                 // Input files (filename -> content)
        "in.png": imageData,
    },
    Stdin: []byte("some input"),              // Optional stdin data
    ResultFilePatterns: []string{"*.png"},    // Glob patterns for result files
    NonErrorExitCodes: map[int]bool{1: true}, // Exit codes to not treat as errors
}
```

### Local Execution

Execute commands locally without a server:

```go
result, callID, err := rcom.ExecuteLocally(ctx, cmd)
if err != nil {
    log.Fatal(err)
}

log.Printf("Call ID: %s", callID)
log.Printf("Output: %s", result.Output)
log.Printf("Stdout: %s", result.Stdout)
log.Printf("Stderr: %s", result.Stderr)

// Access result files
for filename, data := range result.Files {
    log.Printf("Result file: %s (%d bytes)", filename, len(data))
}
```

### Remote Execution

Execute commands on a remote server:

```go
result, err := rcom.ExecuteRemotely(ctx, "http://localhost:8080", cmd)
if err != nil {
    log.Fatal(err)
}
```

### Using Executor Interface

`LocalExecutor` and `RemoteExecutor` implement the `Executor` interface,
so code can switch between local and remote execution:

```go
var exec rcom.Executor

// Use local execution
exec = rcom.LocalExecutor()

// Or execute on an rcom server
exec = rcom.RemoteExecutor("http://localhost:8080")

result, err := exec.Execute(ctx, cmd)
```

### Client Options

Configure clients with functional options:

```go
client := rcom.NewClient(
    rcom.ClientWithHost("example.com"),    // Set server host
    rcom.ClientWithPort(8080),             // Set server port
    rcom.ClientWithCmds("convert", "ffmpeg"), // Allowed commands
    rcom.ClientWithTimeOut(60 * time.Second), // Request timeout
)
```

### Multiple Commands

For clients that support multiple commands:

```go
client := rcom.NewClient(
    rcom.ClientWithHost("localhost"),
    rcom.ClientWithPort(8080),
    rcom.ClientWithCmds("convert", "ffmpeg"),
)

// Execute specific command
result, err := client.ExecuteWithCommand(
    ctx,
    "convert",                                    // which command
    []string{"-resize", "50%", "in.png", "out.png"}, // args
    []fs.FileReader{inputFile},                   // input files
    "out.png",                                    // result file patterns
)
```

### Stdin Support

Provide standard input to commands:

```go
cmd := &rcom.Command{
    Name:  "base64",
    Args:  []string{"-d"},
    Stdin: []byte("SGVsbG8gV29ybGQh"),
}

result, _, err := rcom.ExecuteLocally(ctx, cmd)
// result.Stdout contains the decoded output
```

### Non-Error Exit Codes

Some commands use non-zero exit codes that shouldn't be treated as errors:

```go
cmd := &rcom.Command{
    Name: "grep",
    Args: []string{"pattern", "file.txt"},
    NonErrorExitCodes: map[int]bool{
        1: true, // grep returns 1 when no matches found
    },
}
```

### Result File Patterns

Use glob patterns to select result files:

```go
cmd := &rcom.Command{
    Name:               "my-generator",
    ResultFilePatterns: []string{"*.json", "output-*.txt", "report.pdf"},
}

result, _, err := rcom.ExecuteLocally(ctx, cmd)

// All matching files are in result.Files
for filename, data := range result.Files {
    log.Printf("Generated: %s (%d bytes)", filename, len(data))
}
```

## Server Configuration

### Standalone Server

`cmd/rcom-server` runs a server with graceful shutdown.
Pass the port and the allowed commands as arguments:

```bash
go install github.com/domonda/go-rcom/cmd/rcom-server@latest
rcom-server 3666 convert ffmpeg
```

An empty port argument (`rcom-server "" convert`) uses the `PORT`
environment variable, or 3666 if it is not set.

### Custom Port

```go
err := rcom.ListenAndServe(3000, true, "allowed-cmd1", "allowed-cmd2")
```

### Disable Graceful Shutdown

```go
err := rcom.ListenAndServe(8080, false, "convert")
```

### Graceful Shutdown Timeout

With graceful shutdown, `ListenAndServe` returns `http.ErrServerClosed`
after the running requests have finished. Requests still running after
`GracefulShutdownTimeout`, or when a second signal arrives, are canceled
and `ListenAndServe` returns the shutdown error. A zero or negative
`GracefulShutdownTimeout` waits for the running requests until a second signal:

```go
rcom.GracefulShutdownTimeout = 2 * time.Minute
err := rcom.ListenAndServe(8080, true, "convert")
```

### Server Limits

`ListenAndServe` applies these package variables as they are set when it is called.
Zero or a negative value means no limit:

| Variable | Default | Effect |
|---|---|---|
| `MaxRequestSize` | 1 GiB | Larger requests are rejected with 413 |
| `MaxConnections` | 256 | Further connections wait to be accepted |
| `StallTimeout` | 1 minute | Ends a request whose client stops sending or receiving |
| `MaxConcurrentCommands` | `GOMAXPROCS` | Further requests wait, and get 503 if the server shuts down |
| `MaxCommandDuration` | 1 hour | Longer running commands are killed |

`ExecuteLocally`, which the server uses, also applies:

| Variable | Default | Effect |
|---|---|---|
| `MaxOutputSize` | 64 MiB | A command writing more to stdout and stderr is killed |
| `MaxResultFilesSize` | 1 GiB | Larger result files together are an error |

```go
rcom.MaxRequestSize = 100 << 20 // 100 MiB
rcom.MaxConcurrentCommands = 4
err := rcom.ListenAndServe(8080, true, "convert")
```

### Command Environment

Commands only inherit the environment variables listed in `InheritedEnvVars`
(`PATH`, `HOME`, locale, temp dir and library path variables by default),
so that secrets in the environment of the server don't reach them.
Add variables a command needs, or set it to nil to inherit all:

```go
rcom.InheritedEnvVars = append(rcom.InheritedEnvVars, "MAGICK_HOME")
```

### Custom Logger

Use a custom logger:

```go
import "github.com/domonda/golog/log"

rcom.SetLogger(log.WithPrefix("rcom-server: "))
```

## Architecture

### How It Works

1. **Client** creates a `Command` with name, args, files, and result patterns
2. **Client** encodes command using `encoding/gob` and sends via HTTP POST
3. **Server** receives request, validates command is allowed,
   and cancels the command if the client closes the connection
4. **Server** creates unique temporary directory for execution
5. **Server** writes input files to temp directory
6. **Server** executes command in temp directory with stdin
7. **Server** collects output, stderr, exit code, and result files matching patterns
8. **Server** cleans up temporary directory
9. **Server** encodes `Result` and sends back to client
10. **Client** decodes result and makes files available

### Temporary Directory

Each command execution gets a unique temporary directory:
- Located in system temp dir, accessible only by the user of the process
- Named with a UUID v7 (time-sortable)
- Input files are written here
- Command executes with this as working directory
- Result files are read from here, skipping symlinks, directories
  and files with more than one hard link
- Directory is cleaned up after execution

### Security Considerations

1. **Command Whitelisting**: Server only executes explicitly allowed command names.
   Arguments are passed through unchanged, so only allow commands whose
   arguments can't read or write files outside of the working directory
   in a harmful way, or validate them in front of the server.
2. **No Path Traversal**: Input file names cannot contain path separators,
   also not URL-encoded, cannot be `.` or `..`, contain NUL or exceed 255 bytes,
   and are written through an `os.Root` of the working directory
3. **Separate Working Directory**: Each command runs in its own temporary directory.
   This is not a sandbox: the command runs with the permissions of the server process
4. **Subprocess Cleanup**: Child processes are killed on cancellation
   and when the command exited (not on Windows)
5. **Input Validation**: Commands are validated before execution
6. **No Authentication**: The server accepts requests from anyone who can reach
   its port over plain HTTP. Run it in a private network or behind an authenticating proxy
7. **Untrusted Requests**: Requests are gob-decoded before the command name is checked.
   The server limits bound the resources a client can use, but gob can allocate
   up to about 10 MiB per connection before that data arrives,
   so `MaxConnections` times 10 MiB of memory can be held by slow clients
8. **Environment**: Commands only inherit the `InheritedEnvVars`

## Error Handling

All errors are returned with context. Common error scenarios:

- **Command not allowed**: Server rejects command not in whitelist
- **Invalid filenames**: Filenames with path separators are rejected
- **Duplicate filenames**: `Client` rejects input files with the same base name.
  File names that differ only in case are rejected on case-insensitive file systems,
  like on macOS and Windows, by the server with 400
- **Request too large**: Requests larger than `MaxRequestSize` are rejected with 413
- **Invalid request**: Invalid commands and data after the command are rejected with 400
- **Shutting down**: Requests waiting for a command slot are rejected with 503
- **Command limits**: Commands running longer than `MaxCommandDuration` or writing
  more than `MaxOutputSize` are killed, and the server responds with 500
- **Non-zero exit codes**: Treated as errors unless in `NonErrorExitCodes`
- **Missing result files**: `Result.WriteTo` returns error if file not found
- **Context cancellation**: Commands respect context cancellation
- **Timeout**: Commands timeout based on client configuration

## Best Practices

1. **Whitelist Commands**: Only allow necessary commands on the server
2. **Use Timeouts**: Always set reasonable timeouts on clients
3. **Validate Results**: Check exit codes and result file presence
4. **Clean Error Handling**: Non-zero exit codes should be explicitly handled
5. **Use Result Patterns**: Specify patterns to avoid returning unnecessary files
6. **Context Propagation**: Always pass context for cancellation support
7. **Unique Call IDs**: Use the returned call ID for logging and debugging

## Example: Image Conversion Service

Server:

```go
package main

import (
    "log"
    "github.com/domonda/go-rcom"
)

func main() {
    // Only allow ImageMagick convert
    err := rcom.ListenAndServe(8080, true, "convert")
    if err != nil {
        log.Fatal(err)
    }
}
```

Client:

```go
package main

import (
    "context"
    "log"
    "time"
    "github.com/domonda/go-rcom"
    "github.com/ungerik/go-fs"
)

func convertImage(inputPath, outputPath string) error {
    ctx := context.Background()

    client := rcom.NewClient(
        rcom.ClientWithHost("conversion-server.local"),
        rcom.ClientWithPort(8080),
        rcom.ClientWithCmds("convert"),
        rcom.ClientWithTimeOut(30 * time.Second),
    )

    inputFile := fs.File(inputPath)

    result, err := client.Execute(
        ctx,
        []string{"-resize", "800x600", inputFile.Name(), "output.jpg"},
        []fs.FileReader{inputFile},
        "output.jpg",
    )
    if err != nil {
        return err
    }

    return result.WriteTo(fs.File(outputPath))
}

func main() {
    err := convertImage("/tmp/input.png", "/tmp/output.jpg")
    if err != nil {
        log.Fatal(err)
    }
    log.Println("Image converted successfully")
}
```

## Testing

Run the test suite:

```bash
go test ./...
```

## Dependencies

- [github.com/domonda/go-types](https://github.com/domonda/go-types) - Type system extensions
- [github.com/domonda/golog](https://github.com/domonda/golog) - Structured logging
- [github.com/ungerik/go-fs](https://github.com/ungerik/go-fs) - File system abstraction

## License

See LICENSE file
