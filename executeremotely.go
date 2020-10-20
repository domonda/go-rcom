package rcom

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
)

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
