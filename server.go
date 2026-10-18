package rcom

import (
	"bufio"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
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
// gob-encoded Result structs. Commands not in allowedCMDs, invalid commands
// and requests with data after the Command are rejected with HTTP 400 Bad Request.
//
// The server applies MaxRequestSize and MaxConcurrentCommands
// as they are set when ListenAndServe is called.
// Requests waiting for a command slot when the server shuts down
// are rejected with HTTP 503 Service Unavailable.
//
// If gracefulShutdown is enabled, the server will on receiving a termination signal:
//   - Stop accepting new connections
//   - Wait for in-flight requests to complete, then return http.ErrServerClosed
//   - Cancel requests still running after GracefulShutdownTimeout,
//     or when a second signal is received, and return the shutdown error
func ListenAndServe(port uint16, gracefulShutdown bool, allowedCMDs ...string) error {
	server := newServer(port, allowedCMDs)
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

// newServer returns the HTTP server for ListenAndServe
func newServer(port uint16, allowedCMDs []string) *http.Server {
	svc := newService(allowedCMDs)
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           svc,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	server.RegisterOnShutdown(svc.shutdown)
	return server
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
	// Closing a connection does not cancel its request in every case,
	// for example not after the client sent more data after the body,
	// so the contexts of all requests are canceled explicitly
	requestsCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	server.BaseContext = func(net.Listener) context.Context { return requestsCtx }

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
		cancelRequests()
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

	// shuttingDown is closed by shutdown
	shuttingDown chan struct{}
	shutdownOnce sync.Once
}

// shutdown rejects requests waiting for a command slot
func (s *service) shutdown() {
	s.shutdownOnce.Do(func() { close(s.shuttingDown) })
}

func newService(allowedCMDs []string) *service {
	s := &service{
		allowedCMDs:    make(map[string]bool),
		maxRequestSize: MaxRequestSize,
		shuttingDown:   make(chan struct{}),
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

	var wire wireCommand
	body := bufio.NewReader(r.Body)
	err := gob.NewDecoder(body).Decode(&wire)
	if err == nil {
		// The request body must end after the command because
		// the server cancels the requests of closed connections
		// only after their body has been read completely
		if _, err = body.Peek(1); err == nil {
			err = errors.New("rcom.Command: unexpected data after the command")
		} else if err == io.EOF {
			err = nil
		}
	}
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
	command, err := wire.command()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	if errors.Is(err, errNoCommandSlot) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, os.ErrExist) {
		// Input file names that differ only in case
		// on a case-insensitive file system
		http.Error(w, err.Error(), http.StatusBadRequest)
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

	err = gob.NewEncoder(w).Encode(newWireResult(result))
	if err != nil {
		log.Error("can't encoding command response").
			Err(err).
			UUID("callID", callID).
			Log()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// errNoCommandSlot is returned by service.execute if the request
// was canceled or the server shut down while waiting for a free command slot
var errNoCommandSlot = errors.New("no free slot for running commands")

// execute executes the command locally. If the number of concurrent
// commands is limited, it waits for a free slot and releases it when
// the command has finished, before the result is sent to the client,
// so that a client reading slowly does not block other commands.
// Waiting requests are rejected when the server shuts down.
func (s *service) execute(ctx context.Context, command *Command) (*Result, uu.ID, error) {
	if s.running != nil {
		// Take a free slot also when the server is shutting down
		// because a select chooses randomly between ready cases
		select {
		case s.running <- struct{}{}:
		default:
			select {
			case s.running <- struct{}{}:
			case <-ctx.Done():
				return nil, uu.IDNil, fmt.Errorf("%w: request canceled while waiting", errNoCommandSlot)
			case <-s.shuttingDown:
				return nil, uu.IDNil, fmt.Errorf("%w: server is shutting down", errNoCommandSlot)
			}
		}
		defer func() { <-s.running }()
	}
	log.Infof("Executing command: %s", command).Log()
	return ExecuteLocally(ctx, command)
}
