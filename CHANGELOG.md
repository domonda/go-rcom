# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are git-tagged with the version from the `VERSION` file.

## [Unreleased]

## [0.1.0] - 2026-10-18

First tagged release.

### Added

- `Command` describes a CLI call: its name, arguments, input files, stdin,
  glob patterns for result files and exit codes that do not count as errors.
  `Result` carries the combined output, stdout, stderr, exit code and the
  matched result files; `Result.WriteTo` saves a result file.
  `Command.Validate` rejects input file names that contain a path separator.
- `ExecuteLocally` runs a `Command` in a fresh temporary directory named with
  a time-sortable UUID v7 call ID, and removes the directory afterwards.
- `ExecuteRemotely` sends a gob-encoded `Command` to an rcom server over HTTP.
- The `Executor` interface, `ExecutorFunc` and `LocalExecutor` let code switch
  between local and remote execution.
- `Client` and `NewClient` with the options `ClientWithHost`,
  `ClientWithPort`, `ClientWithCmds` and `ClientWithTimeOut`.
- `ListenAndServe` starts an HTTP server that only runs whitelisted commands
  and optionally shuts down gracefully on SIGTERM, SIGINT and SIGHUP within
  `GracefulShutdownTimeout`. `SetLogger` replaces the package logger.
- `pkg/exec` wraps `os/exec` with builder methods and can kill a command's
  subprocesses when its context is canceled.

### Changed

- Requires Go 1.26.0.
- Depends on tagged releases: go-types v1.0.0-beta.2, golog v1.3.0 and
  go-fs v0.1.0.
