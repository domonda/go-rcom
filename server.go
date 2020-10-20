package rcom

import (
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

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
	if gracefulShutdown {
		gracefullyShutdownServerOnSignal(server, GracefulShutdownTimeout)
	}
	return server.ListenAndServe()
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

func gracefullyShutdownServerOnSignal(server *http.Server, timeout time.Duration, signals ...os.Signal) {
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM}
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, signals...)
	go func() {
		sig := <-shutdown
		log.Debugf("Received signal %s", sig).Log()

		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		err := server.Shutdown(ctx)
		if err != nil {
			log.Error("Server shutdown error").Err(err).Log()
		}
	}()
}
