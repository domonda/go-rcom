package rcom

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/domonda/go-types/uu"
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
// The server applies MaxRequestSize and MaxConcurrentCommands
// as they are set when ListenAndServe is called.
//
// If gracefulShutdown is enabled, the server will on receiving a termination signal:
//   - Stop accepting new connections
//   - Wait for in-flight requests to complete, then return http.ErrServerClosed
//   - Cancel requests still running after GracefulShutdownTimeout,
//     or when a second signal is received, and return the shutdown error
func ListenAndServe(port uint16, gracefulShutdown bool, allowedCMDs ...string) error {
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           newService(allowedCMDs),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
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
// after all requests have finished. Requests still running after timeout,
// or when a second signal is received, are canceled, and the shutdown
// error is returned after their handlers have cleaned up.
func serveUntilSignal(server *http.Server, listener net.Listener, signals <-chan os.Signal, timeout time.Duration) error {
	// Track the handlers to wait for their cleanup
	// after their requests have been canceled
	var handlers sync.WaitGroup
	handler := server.Handler
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		handler.ServeHTTP(w, r)
	})

	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	select {
	case err := <-served:
		return err
	case sig := <-signals:
		log.Debugf("Received signal %s", sig).Log()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// A second signal cancels the graceful shutdown
	go func() {
		select {
		case sig := <-signals:
			log.Debugf("Received second signal %s", sig).Log()
			cancel()
		case <-ctx.Done():
		}
	}()

	err := server.Shutdown(ctx)
	if err != nil {
		log.Error("Server shutdown error").Err(err).Log()
		// Cancel the requests that are still running
		// and give their handlers time to clean up
		server.Close()
		cleanedUp := make(chan struct{})
		go func() {
			handlers.Wait()
			close(cleanedUp)
		}()
		select {
		case <-cleanedUp:
		case <-time.After(handlerCleanupTimeout):
			log.Error("Handlers still running after shutdown").Log()
		}
		return fmt.Errorf("rcom server shutdown: %w", err)
	}
	return http.ErrServerClosed
}

const (
	// Timeouts of the server started by ListenAndServe.
	// There is no timeout for reading the request body
	// because input files can be large.
	serverReadHeaderTimeout = 10 * time.Second
	serverIdleTimeout       = 2 * time.Minute

	// handlerCleanupTimeout is how long a canceled shutdown
	// waits for the handlers of canceled requests to clean up
	handlerCleanupTimeout = 10 * time.Second
)

type service struct {
	allowedCMDs map[string]bool

	// maxRequestSize limits the request size if greater than zero
	maxRequestSize int64

	// running limits the number of concurrently executed
	// commands to its capacity if not nil
	running chan struct{}
}

func newService(allowedCMDs []string) *service {
	s := &service{
		allowedCMDs:    make(map[string]bool),
		maxRequestSize: MaxRequestSize,
	}
	for _, cmd := range allowedCMDs {
		s.allowedCMDs[cmd] = true
	}
	if MaxConcurrentCommands > 0 {
		s.running = make(chan struct{}, MaxConcurrentCommands)
	}
	return s
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if s.maxRequestSize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxRequestSize)
	}

	var command *Command
	err := gob.NewDecoder(r.Body).Decode(&command)
	if err != nil {
		log.Error("can't decode command request").
			Err(err).
			Log()
		status := http.StatusBadRequest
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}

	if !s.allowedCMDs[command.Name] {
		http.Error(w, fmt.Sprintf("command %q not allowed", command.Name), http.StatusBadRequest)
		return
	}
	err = command.Validate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, callID, err := s.execute(r.Context(), command)
	if errors.Is(err, errWaitingForCommandSlot) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
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

// errWaitingForCommandSlot is returned by service.execute if the
// request was canceled while waiting for a free command slot
var errWaitingForCommandSlot = errors.New("request canceled while waiting for running commands")

// execute executes the command locally. If the number of concurrent
// commands is limited, it waits for a free slot and releases it when
// the command has finished, before the result is sent to the client,
// so that a client reading slowly does not block other commands.
func (s *service) execute(ctx context.Context, command *Command) (*Result, uu.ID, error) {
	if s.running != nil {
		select {
		case s.running <- struct{}{}:
			defer func() { <-s.running }()
		case <-ctx.Done():
			return nil, uu.IDNil, errWaitingForCommandSlot
		}
	}
	log.Infof("Executing command: %s", command).Log()
	return ExecuteLocally(ctx, command)
}
