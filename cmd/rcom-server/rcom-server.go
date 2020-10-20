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

	portStr := os.Getenv("PORT")
	if len(os.Args) > 1 {
		portStr = os.Args[1]
	}
	if portStr == "" {
		portStr = "3666"
	}

	var port uint16
	_, err := fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		log.Fatal("Can't scan port number").Err(err).LogAndPanic()
	}

	log.Infof("Listening on port %d", port).Log()
	err = rcom.ListenAndServe(port, true)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Server error").Err(err).LogAndPanic()
	}
}
