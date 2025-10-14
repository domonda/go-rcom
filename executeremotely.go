package rcom

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
)

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
//   - Server returns non-200 status (e.g., command not allowed)
//   - Response decoding fails
//
// The server must be running ListenAndServe with the command allowed.
// The context is respected for request cancellation and timeout.
func ExecuteRemotely(ctx context.Context, addr string, c *Command) (result *Result, err error) {
	log.Debug("ExecuteRemotely").
		Str("addr", addr).
		Str("command", c.Name).
		Log()

	err = c.Validate()
	if err != nil {
		return nil, err
	}

	buf := bytes.NewBuffer(nil)
	err = gob.NewEncoder(buf).Encode(c)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, "POST", addr, buf)
	if err != nil {
		return nil, err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rcom.Command: response status %s", response.Status)
	}

	defer response.Body.Close()
	err = gob.NewDecoder(response.Body).Decode(&result)
	if err != nil {
		return nil, err
	}

	return result, nil
}
