package rcom

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/domonda/golog"
)

// maxErrorResponseSize limits how much of an error response
// ExecuteRemotely reads into the returned error
// and is larger than the output of a failed command in errors
const maxErrorResponseSize = 128 << 10

// ExecuteRemotely executes a command on a remote rcom server via HTTP.
//
// The function:
//  1. Validates the command
//  2. Encodes the command using gob encoding
//  3. Sends an HTTP POST request to the server address
//  4. Receives and decodes the gob-encoded result
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - addr: Full server address including scheme and port (e.g., "http://localhost:8080")
//   - c: The command to execute
//
// Returns an error if:
//   - Command validation fails
//   - Network request fails
//   - Server returns non-200 status (e.g., command not allowed),
//     as *ResponseError with the server's error message
//   - Response decoding fails
//
// The server must be running ListenAndServe with the command allowed.
// The request ID of a golog context attribute "requestID" is sent
// to the server as X-Request-ID header.
// The context is respected for request cancellation and timeout.
func ExecuteRemotely(ctx context.Context, addr string, c *Command) (result *Result, err error) {
	log.DebugCtx(ctx, "ExecuteRemotely").
		Str("addr", addr).
		Str("command", c.Name).
		Log()

	err = c.Validate()
	if err != nil {
		return nil, err
	}

	buf := bytes.NewBuffer(nil)
	err = writeWireCommand(buf, c)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, "POST", addr, buf)
	if err != nil {
		return nil, err
	}
	// Pass on the request ID for the logs of the server
	if requestID, ok := golog.GetRequestUUIDFromContext(ctx); ok {
		request.Header.Set("X-Request-ID", golog.FormatUUID(requestID))
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		// The server responds with an error message
		message, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorResponseSize))
		return nil, &ResponseError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Message:    string(bytes.TrimSpace(message)),
		}
	}

	return readWireResult(response.Body)
}

// ResponseError is returned by ExecuteRemotely
// if the server responded with an error status
type ResponseError struct {
	StatusCode int    // For example 500
	Status     string // For example "500 Internal Server Error"
	Message    string // The error message of the server
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("rcom.Command: response status %s: %s", e.Status, e.Message)
}
