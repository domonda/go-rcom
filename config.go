package rcom

import (
	"runtime"
	"time"

	"github.com/domonda/golog"
	rootlog "github.com/domonda/golog/log"
)

var (
	// GracefulShutdownTimeout is how long ListenAndServe waits
	// for running requests after a termination signal.
	// Zero or a negative value waits until a second signal.
	GracefulShutdownTimeout = time.Minute

	// MaxRequestSize is the maximum size in bytes of a request,
	// including all input files, that ListenAndServe accepts.
	// Larger requests are rejected with HTTP 413 Request Entity Too Large.
	// Zero or a negative value means no limit.
	MaxRequestSize int64 = 1 << 30

	// MaxConcurrentCommands is the maximum number of commands
	// that ListenAndServe executes at the same time.
	// Further requests wait until a running command has finished.
	// Zero or a negative value means no limit.
	// The default is GOMAXPROCS, which follows container CPU limits.
	MaxConcurrentCommands = runtime.GOMAXPROCS(0)

	// MaxCommandDuration is the maximum time a command executed
	// by ListenAndServe may run before it is killed.
	// Zero or a negative value means no limit.
	MaxCommandDuration = time.Hour

	// MaxConnections is the maximum number of connections
	// that ListenAndServe accepts at the same time.
	// Further connections wait until an accepted one is closed.
	// Zero or a negative value means no limit.
	MaxConnections = 256

	// StallTimeout is how long ListenAndServe waits for a client
	// to send more of its request or to receive more of the response
	// before closing the connection.
	// Zero or a negative value means no timeout.
	StallTimeout = time.Minute

	// MinTransferRate is the minimum average rate in bytes per second
	// at which ListenAndServe receives a request and sends a response,
	// starting after StallTimeout, so that slow clients
	// can't hold connections and memory for long.
	// Zero or a negative value, or no StallTimeout, means no minimum.
	MinTransferRate int64 = 16 << 10

	// MaxOutputSize is the maximum number of bytes that a command
	// executed by ExecuteLocally may write to stdout and stderr together.
	// A command writing more is killed and an error is returned.
	// Zero or a negative value means no limit.
	MaxOutputSize int64 = 64 << 20

	// MaxResultFilesSize is the maximum total size in bytes
	// of the result files that ExecuteLocally reads.
	// Zero or a negative value means no limit.
	MaxResultFilesSize int64 = 1 << 30

	// InheritedEnvVars are the environment variables that commands
	// executed by ExecuteLocally inherit from the process,
	// so that secrets in the environment of a server don't reach them.
	// Names are case-insensitive on Windows.
	// Nil means all variables are inherited.
	InheritedEnvVars = []string{
		"PATH", "HOME", "USER", "LOGNAME", "LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "TZ",
		"TMPDIR", "TMP", "TEMP", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH",
		"SYSTEMROOT", "WINDIR", "PATHEXT", "COMSPEC", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES",
	}

	log = rootlog.NewPackageLogger()
)

// SetLogger changes the logger used by the package.
// Passing nil disables logging.
// The default is github.com/domonda/golog/log.NewPackageLogger(),
// which prefixes messages with "go-rcom: ".
func SetLogger(l *golog.Logger) {
	log = l
}
