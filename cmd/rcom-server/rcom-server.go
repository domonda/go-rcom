// rcom-server runs an rcom server that executes the allowed commands.
//
// Usage:
//
//	rcom-server PORT ALLOWED_COMMAND...
//
// If PORT is an empty string, the PORT environment variable
// or the default port 3666 is used. Every allowed command
// must be found in the PATH. GET /health responds with 200 OK.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	"github.com/domonda/go-rcom"
	"github.com/domonda/golog/log"
)

func main() {
	log := log.WithPrefix("rcom-server: ")

	port, allowedCmds, err := parseArgs(os.Args[1:], os.Getenv("PORT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "rcom-server:", err)
		fmt.Fprintln(os.Stderr, "usage: rcom-server PORT ALLOWED_COMMAND...")
		os.Exit(2)
	}

	err = checkCommands(allowedCmds)
	if err != nil {
		log.Error("Allowed command not found").Err(err).Log()
		os.Exit(1)
	}

	log.Infof("Listening on port %d, allowed commands: %v", port, allowedCmds).Log()
	err = rcom.ListenAndServe(port, true, allowedCmds...)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("Server error").Err(err).Log()
		os.Exit(1)
	}
}

// parseArgs returns the port and allowed commands from the arguments,
// using envPort or 3666 for an empty port argument
func parseArgs(args []string, envPort string) (port uint16, allowedCmds []string, err error) {
	if len(args) < 2 {
		return 0, nil, errors.New("missing arguments")
	}
	portStr := args[0]
	if portStr == "" {
		portStr = envPort
	}
	if portStr == "" {
		portStr = "3666"
	}
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid port %q", portStr)
	}
	return uint16(p), args[1:], nil
}

// checkCommands returns an error if a command is not found in the PATH
func checkCommands(cmds []string) error {
	for _, cmd := range cmds {
		_, err := exec.LookPath(cmd)
		if err != nil {
			return err
		}
	}
	return nil
}
