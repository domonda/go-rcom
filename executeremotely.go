package rcom

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"

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
// The golog request ID of ctx, see golog.ContextWithRequestUUID,
// is sent to the server as X-Request-ID header, which the server
// uses for its logs if it is a UUID.
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
	body, err := encodeCommand(c)
	if err != nil {
		return nil, err
	}
	return postCommand(ctx, addr, body)
}

// encodeCommand returns the request body for the command
func encodeCommand(c *Command) ([]byte, error) {
	// Sized for the data and the header, so that large
	// input files are not copied while the buffer grows
	size := 4<<10 + len(c.Name) + len(c.Stdin)
	for _, s := range slices.Concat(c.Args, c.ResultFilePatterns) {
		size += len(s) + 10
	}
	for name, value := range c.Env {
		size += len(name) + len(value) + 10
	}
	for name, data := range c.Files {
		size += len(name) + len(data) + 16
	}
	var buf bytes.Buffer
	buf.Grow(size)
	err := writeWireCommand(&buf, c)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// postCommand sends the encoded command to the server at addr
// and returns its result. The error of a request that got
// no response is a *transportError.
func postCommand(ctx context.Context, addr string, body []byte) (*Result, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Pass on the request ID for the logs of the server
	requestID := golog.GetRequestIDFromContext(ctx)
	if requestID != "" {
		request.Header.Set(requestIDHeader, requestID)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, &transportError{err: err}
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		// The server responds with an error message
		message, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorResponseSize))
		if id := response.Header.Get(requestIDHeader); id != "" {
			requestID = id
		}
		return nil, &ResponseError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Message:    string(bytes.TrimSpace(message)),
			RequestID:  requestID,
		}
	}

	return readWireResult(response.Body)
}

// transportError is the error of a request that got no response
type transportError struct {
	err error
}

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// ResponseError is returned by ExecuteRemotely
// if the server responded with an error status
type ResponseError struct {
	StatusCode int    // For example 500
	Status     string // For example "500 Internal Server Error"
	Message    string // The error message of the server
	RequestID  string // The X-Request-ID response header for the logs of the server, else the sent one
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("rcom.Command: response status %s: %s", e.Status, e.Message)
}
