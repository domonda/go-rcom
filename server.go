package rcom

import (
	"context"
	"encoding/gob"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ListenAndServe starts an HTTP server that accepts and executes remote commands.
// The server only executes commands explicitly listed in allowedCMDs.
//
// Parameters:
//   - port: The port number to listen on
//   - gracefulShutdown: If true, enables graceful shutdown on SIGTERM/SIGINT/SIGHUP
//   - allowedCMDs: Command names that are allowed to be executed
//
// The server receives gob-encoded Command structs via HTTP POST and returns
// gob-encoded Result structs. Commands not in allowedCMDs are rejected with
// HTTP 400 Bad Request.
//
// If gracefulShutdown is enabled, the server will on receiving a termination signal:
//   - Stop accepting new connections
//   - Wait for in-flight requests to complete, then return http.ErrServerClosed
//   - Cancel requests still running after GracefulShutdownTimeout
//     and return the shutdown error
func ListenAndServe(port uint16, gracefulShutdown bool, allowedCMDs ...string) error {
	cmds := make(map[string]bool)
	for _, cmd := range allowedCMDs {
		cmds[cmd] = true
	}
	svc := &service{allowedCMDs: cmds}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: svc,
	}
	if !gracefulShutdown {
		return server.ListenAndServe()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return serveUntilSignal(server, listener, signals, GracefulShutdownTimeout)
}

// serveUntilSignal serves on listener until a signal is received,
// then shuts the server down gracefully and returns http.ErrServerClosed
// after all requests have finished. Requests still running after timeout
// are canceled and the shutdown error is returned.
func serveUntilSignal(server *http.Server, listener net.Listener, signals <-chan os.Signal, timeout time.Duration) error {
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	select {
	case err := <-served:
		return err
	case sig := <-signals:
		log.Debugf("Received signal %s", sig).Log()
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	err := server.Shutdown(ctx)
	if err != nil {
		log.Error("Server shutdown error").Err(err).Log()
		// Cancel the requests that are still running
		server.Close()
		return fmt.Errorf("rcom server shutdown: %w", err)
	}
	return http.ErrServerClosed
}

type service struct {
	allowedCMDs map[string]bool
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var command *Command
	err := gob.NewDecoder(r.Body).Decode(&command)
	if err != nil {
		log.Error("can't decode command request").
			Err(err).
			Log()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !s.allowedCMDs[command.Name] {
		http.Error(w, fmt.Sprintf("command %q not allowed", command.Name), http.StatusBadRequest)
		return
	}

	log.Infof("Executing command: %s", command).Log()
	result, callID, err := ExecuteLocally(r.Context(), command)
	if err != nil {
		log.Error("error while executing command").
			Err(err).
			UUID("callID", callID).
			Log()
		http.Error(w, fmt.Sprintf("%s\n\n%s", command, err), http.StatusInternalServerError)
		return
	}

	err = gob.NewEncoder(w).Encode(result)
	if err != nil {
		log.Error("can't encoding command response").
			Err(err).
			UUID("callID", callID).
			Log()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
