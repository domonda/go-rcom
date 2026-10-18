// rcom-server runs an rcom server that executes the allowed commands.
//
// Usage:
//
//	rcom-server PORT ALLOWED_COMMAND...
//
// If PORT is an empty string, the PORT environment variable
// or the default port 3666 is used.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/domonda/go-rcom"
	"github.com/domonda/golog/log"
)

func main() {
	log := log.WithPrefix("rcom-server: ")

	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: rcom-server PORT ALLOWED_COMMAND...")
		os.Exit(2)
	}
	portStr := os.Args[1]
	allowedCmds := os.Args[2:]
	if portStr == "" {
		portStr = os.Getenv("PORT")
	}
	if portStr == "" {
		portStr = "3666"
	}

	var port uint16
	_, err := fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		log.Fatal("Can't scan port number").Err(err).LogAndPanic()
	}

	log.Infof("Listening on port %d, allowed commands: %v", port, allowedCmds).Log()
	err = rcom.ListenAndServe(port, true, allowedCmds...)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Server error").Err(err).LogAndPanic()
	}
}
