package rcom

import (
	"bufio"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// response writes the response to a request of the service.
// After hijackConnection it writes to the hijacked connection.
type response struct {
	w            http.ResponseWriter
	conn         net.Conn // Not nil after hijacking
	stallTimeout time.Duration
}

// hijackConnection takes over the connection of the request if the
// ResponseWriter supports it and returns a context that is canceled
// when the client closes the connection or sends more data.
// net/http does not cancel the request context when a client
// closes the connection after sending more data than the request.
func (resp *response) hijackConnection(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	conn, buf, err := http.NewResponseController(resp.w).Hijack()
	if err != nil {
		// Not supported, for example by httptest.ResponseRecorder
		return ctx, cancel
	}
	resp.conn = conn
	// Remove the stall deadline of reading the request
	_ = conn.SetReadDeadline(time.Time{})
	go func() {
		// The client sends nothing until it received the response,
		// so the read returns when the client closed the connection
		// or sent data that is not part of the protocol,
		// or when the connection was closed after the response
		_, _ = buf.ReadByte()
		cancel()
	}()
	return ctx, cancel
}

func (resp *response) error(status int, message string) {
	if resp.conn == nil {
		http.Error(resp.w, message, status)
		return
	}
	_ = resp.writeHijacked(status, "text/plain; charset=utf-8", func(w io.Writer) error {
		_, err := fmt.Fprintln(w, message)
		return err
	})
}

func (resp *response) result(result *Result) error {
	encode := func(w io.Writer) error {
		return gob.NewEncoder(w).Encode(newWireResult(result))
	}
	if resp.conn == nil {
		controller := http.NewResponseController(resp.w)
		// The deadline must not apply to the next request of the connection
		defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
		return encode(&stallWriter{w: resp.w, setDeadline: controller.SetWriteDeadline, timeout: resp.stallTimeout})
	}
	return resp.writeHijacked(http.StatusOK, "application/octet-stream", encode)
}

// writeHijacked writes an HTTP/1.1 response whose body
// ends with the connection, which is closed afterwards
func (resp *response) writeHijacked(status int, contentType string, writeBody func(io.Writer) error) error {
	defer resp.conn.Close()

	w := bufio.NewWriter(&stallWriter{w: resp.conn, setDeadline: resp.conn.SetWriteDeadline, timeout: resp.stallTimeout})
	_, err := fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: %s\r\nX-Content-Type-Options: nosniff\r\nConnection: close\r\n\r\n", status, http.StatusText(status), contentType)
	if err != nil {
		return err
	}
	err = writeBody(w)
	if err != nil {
		return err
	}
	return w.Flush()
}

// stallReader sets a read deadline before every read,
// so that a client that stops sending ends the request
type stallReader struct {
	r           io.Reader
	setDeadline func(time.Time) error
	timeout     time.Duration
}

func (s *stallReader) Read(p []byte) (int, error) {
	if s.timeout > 0 {
		_ = s.setDeadline(time.Now().Add(s.timeout))
	}
	return s.r.Read(p)
}

// stallWriter sets a write deadline before every write,
// so that a client that stops receiving ends the request
type stallWriter struct {
	w           io.Writer
	setDeadline func(time.Time) error
	timeout     time.Duration
}

func (s *stallWriter) Write(p []byte) (int, error) {
	if s.timeout > 0 {
		_ = s.setDeadline(time.Now().Add(s.timeout))
	}
	return s.w.Write(p)
}
