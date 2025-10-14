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
- **Isolated Execution**: Each command runs in a unique temporary directory
- **Context Support**: Full context.Context support for cancellation

## Installation

```bash
go get github.com/domonda/go-rcom
```

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

Both local and remote execution implement the `Executor` interface:

```go
var exec rcom.Executor

// Use local execution
exec = rcom.LocalExecutor()

// Or create a client that implements Executor
client := rcom.NewClient(
    rcom.ClientWithHost("localhost"),
    rcom.ClientWithPort(8080),
    rcom.ClientWithCmds("convert"),
)
// Client implements Executor via ExecuteWithCommand

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

### Custom Port

```go
err := rcom.ListenAndServe(3000, true, "allowed-cmd1", "allowed-cmd2")
```

### Disable Graceful Shutdown

```go
err := rcom.ListenAndServe(8080, false, "convert")
```

### Graceful Shutdown Timeout

Configure the timeout for graceful shutdown:

```go
rcom.GracefulShutdownTimeout = 2 * time.Minute
err := rcom.ListenAndServe(8080, true, "convert")
```

### Custom Logger

Use a custom logger:

```go
import "github.com/domonda/golog"

logger := golog.NewLogger("rcom-server")
rcom.SetLogger(logger)
```

## Architecture

### How It Works

1. **Client** creates a `Command` with name, args, files, and result patterns
2. **Client** encodes command using `encoding/gob` and sends via HTTP POST
3. **Server** receives request, validates command is allowed
4. **Server** creates unique temporary directory for execution
5. **Server** writes input files to temp directory
6. **Server** executes command in temp directory with stdin
7. **Server** collects output, stderr, exit code, and result files matching patterns
8. **Server** encodes `Result` and sends back to client
9. **Server** cleans up temporary directory
10. **Client** decodes result and makes files available

### Temporary Directory

Each command execution gets a unique temporary directory:
- Located in system temp dir
- Named with a UUID v7 (time-sortable)
- Input files are written here
- Command executes with this as working directory
- Result files are read from here
- Directory is cleaned up after execution

### Security Considerations

1. **Command Whitelisting**: Server only executes explicitly allowed commands
2. **No Path Traversal**: Filenames cannot contain path separators
3. **Isolated Execution**: Each command runs in its own temporary directory
4. **Subprocess Cleanup**: Child processes are killed on cancellation
5. **Input Validation**: Commands are validated before execution

## Error Handling

All errors are returned with context. Common error scenarios:

- **Command not allowed**: Server rejects command not in whitelist
- **Invalid filenames**: Filenames with path separators are rejected
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
