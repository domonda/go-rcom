package rcom

import (
	"time"

	"github.com/domonda/golog"
	rootlog "github.com/domonda/golog/log"
)

var (
	GracefulShutdownTimeout = time.Minute

	log = rootlog.NewPackageLogger()
)

// SetLogger changes the logger used by the package.
// Passing nil disables logging.
// The default is github.com/domonda/golog/log.NewPackageLogger("rcom")
func SetLogger(l *golog.Logger) {
	log = l
}
