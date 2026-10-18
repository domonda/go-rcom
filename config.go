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

	log = rootlog.NewPackageLogger()
)

// SetLogger changes the logger used by the package.
// Passing nil disables logging.
// The default is github.com/domonda/golog/log.NewPackageLogger("rcom")
func SetLogger(l *golog.Logger) {
	log = l
}
