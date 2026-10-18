# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are git-tagged with the version from the `VERSION` file.

## [Unreleased]

## [0.1.0] - 2026-10-18

First tagged release. It requires Go 1.26 and depends on tagged releases:
go-types v1.0.0-beta.2, golog v1.3.0 and go-fs v0.1.0. The Changed, Fixed and
Security sections compare with the earlier untagged builds.

### Added

- `Command` describes a CLI call: its name, arguments, input files, stdin,
  glob patterns for result files and exit codes that do not count as errors.
  `Result` carries the combined output, stdout, stderr, exit code and the
  matched result files; `Result.WriteTo` saves a non-empty result file.
- `ExecuteLocally` runs a `Command` in its own temporary working directory,
  named with a time-sortable UUID v7 call ID, and removes it afterwards.
- `ExecuteRemotely` sends a gob-encoded `Command` to an rcom server over HTTP.
- `RemoteExecutor` joins `LocalExecutor` as an `Executor`, so code can switch
  between local and remote execution.
- `Client` and `NewClient` with the options `ClientWithHost`,
  `ClientWithPort`, `ClientWithCmds`, `ClientWithTimeout` and
  `ClientWithRetries`, which retries requests that did not reach the server
  or got 502, 503 or 504, but not failed commands. `ClientWithTimeOut` is a
  deprecated alias of `ClientWithTimeout`.
- `Client.Cmd` builds a command with `WithArgs`, `WithStringArgs`,
  `WithFiles`, `WithStdin`, `WithEnv`, `WithResultFilePatterns` and
  `WithNonErrorExitCodes` and executes it with `Run`, the API of the
  domonda-service rcom package. `WithArgs` sends `fs.FileReader` arguments
  as input files.
- `Command.Env` sets environment variables for a command; the server only
  accepts the `AllowedCommandEnvVars`.
- `Result.FileData` and `Result.MemFile` return a result file, and
  `ExecuteRemotely` returns server errors as `*ResponseError`.
- `GET /health` responds with 200 OK, and the `X-Request-ID` header is passed
  on from the golog `requestID` of the client's context to the server's logs
  and response.
- `ListenAndServe` serves the whitelisted command names (their arguments are
  not restricted) with optional graceful shutdown on SIGTERM, SIGINT and
  SIGHUP. `SetLogger` replaces the package logger.
- Server limits: `MaxRequestSize` (default 1 GiB, larger requests get HTTP
  413), `MaxConnections` (256, further connections wait), `StallTimeout`
  (1 minute without the client sending or receiving ends the request),
  `MinTransferRate` (16 KiB/s after `StallTimeout`), `MaxConcurrentCommands`
  (`GOMAXPROCS`, further requests wait) and `MaxCommandDuration` (1 hour).
  At most twice `MaxConcurrentCommands` requests hold results in memory while
  sending them, and commands have at most 10000 files. The server also sets
  header read and idle timeouts, and cancels a command when its client
  closes the connection.
- `ExecuteLocally` limits: `MaxOutputSize` (64 MiB of stdout and stderr, the
  command is killed above) and `MaxResultFilesSize` (1 GiB). It stops waiting
  for the output 2 s after the command exited, and errors contain only the
  last 64 KiB of the output.
- `InheritedEnvVars` lists the environment variables commands inherit.
- `pkg/exec` wraps `os/exec` with builder methods, can kill a command's
  subprocesses when its context is canceled or after it exited, and has
  `WithWaitDelay` and `WithMaxOutputSize`. `Run` returns after a killed
  process has ended. It also builds on the BSDs.
- `rcom-server` checks at the start that the allowed commands are installed.

### Changed

- **Breaking:** commands and results are sent as a stream of gob messages, a
  header of at most 1 MiB and one message per file, so clients and servers of
  this release can't talk to earlier untagged builds or to the domonda-service
  rcom package. Successful responses use chunked encoding.
- **Breaking:** commands only inherit the `InheritedEnvVars` (`PATH`,
  `HOME`, locale, temp dir and library path variables) instead of the whole
  environment. Set it to nil to inherit all variables.
- **Breaking:** `rcom-server` takes the port and the allowed commands as
  arguments: `rcom-server PORT ALLOWED_COMMAND...`. An empty port uses the
  `PORT` environment variable or 3666. Without commands or with an invalid
  port it exits with status 2.
- **Breaking (via go-types):** call IDs now store their UUID v7 timestamp
  big-endian as RFC 9562 requires, so they sort by time. Call IDs from
  earlier untagged builds do not sort against new ones and decode to wrong
  times. go-types also changed `uu.ID` methods such as `V7Time`, which now
  returns `(time.Time, bool)`.
- With graceful shutdown, `ListenAndServe` returns `http.ErrServerClosed`
  after the running requests have finished. Requests still running after
  `GracefulShutdownTimeout`, or when a second signal arrives, are canceled
  and the shutdown error is returned after their cleanup.
- `Command.Validate` also rejects file names that are `.` or `..`, contain
  NUL, are longer than 255 bytes or contain a URL-encoded path separator,
  and malformed result file patterns.
- The client sends each input file under its base name, also when the name
  is a Windows path like the file name of a browser upload, and returns an
  error for two input files with the same base name.
- The server answers invalid commands and requests with data after the
  command with 400, and requests waiting for a command slot during shutdown
  with 503.
- Result files are regular files only, checked on the opened file; symlinks,
  directories, FIFOs and hard links to files outside of the working directory
  are skipped.
- The working directory is created with mode 0700 and input files with 0600,
  instead of 0771 and 0660 (on unix systems).
- A command ended by a signal reports it, for example
  `ended by signal: killed`.
- Requests other than POST and `/health` get 405.
- Errors of `ExecuteRemotely` contain the server's error message.

### Fixed

- `NonErrorExitCodes` had no effect: every non-zero exit code was an error,
  with the message `%!w(<nil>)`.
- `ListenAndServe` returned as soon as a graceful shutdown started, so
  programs exited while commands were still running.
- `rcom-server` rejected every command.
- A data race in `pkg/exec` lost bytes of `Result.Output` when a command
  wrote to stdout and stderr.
- `ExecuteRemotely` leaked a connection for every non-200 response.
- The package did not build on Windows (`copyCmd redeclared`).
- Errors while collecting result files were ignored.
- IPv6 hosts produced an invalid client URL.
- `Result.WriteTo` reported an empty result file of a remote command as
  missing.
- Input file names that differ only in case overwrote each other on macOS
  and Windows; they are now an error.

### Security

- Input file names like `..%2Fx` escaped the working directory because go-fs
  URL-decodes paths. Input files are now written and result files read
  through an `os.Root` of the working directory.
- Cleanup no longer deletes through symlinks the command created, and result
  collection no longer follows them out of the working directory.
- A request of a few hundred bytes could make the server allocate hundreds of
  MiB, because gob allocates maps from the entry count a message declares, and
  slices of empty elements 40 to 80 times their size.
- Clients that stop sending or receiving held connections and memory
  indefinitely, and a client that disconnected after sending more data than
  its request left its command running.
- Commands inherited secrets from the environment of the server.
- The server has no authentication and decodes requests before checking the
  command name; see the security considerations in the README.
