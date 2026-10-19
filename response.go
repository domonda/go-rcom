package rcom

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"
)

// response writes the response to a request of the service.
// After hijackConnection it writes to the hijacked connection.
type response struct {
	w         http.ResponseWriter
	conn      net.Conn // Not nil after hijacking
	closeOnce sync.Once
	transfer  transferLimits
	requestID string
}

// hijackConnection takes over the connection of the request if the
// ResponseWriter supports it and returns a context that is canceled
// when the client closes the connection or sends more data.
// net/http does not cancel the request context when a client
// closes the connection after sending more data than the request.
func (resp *response) hijackConnection(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
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
		// except line breaks that HTTP/1.1 tolerates after a request,
		// so the read returns when the client closed the connection
		// or sent data that is not part of the protocol,
		// or when the connection was closed after the response
		for {
			b, err := buf.ReadByte()
			if err != nil || (b != '\r' && b != '\n') {
				break
			}
		}
		cancel()
	}()
	return ctx, cancel
}

// close closes a hijacked connection, which net/http does not do
// if the handler panics
func (resp *response) close() {
	if resp.conn != nil {
		resp.closeOnce.Do(func() { _ = resp.conn.Close() })
	}
}

func (resp *response) error(status int, message string) {
	if resp.conn == nil {
		// Limit how long writing the error may stall,
		// net/http resets the deadline for the next request
		controller := http.NewResponseController(resp.w)
		_ = controller.SetWriteDeadline(resp.transfer.deadline(len(message)))
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
		return writeWireResult(w, result)
	}
	if resp.conn == nil {
		controller := http.NewResponseController(resp.w)
		// The deadline must not apply to the next request of the connection
		defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
		return encode(&stallWriter{w: resp.w, setDeadline: controller.SetWriteDeadline, transfer: resp.transfer})
	}
	return resp.writeHijacked(http.StatusOK, "application/octet-stream", encode)
}

// writeHijacked writes an HTTP/1.1 response with a chunked body,
// so that a client or proxy can detect a body that ended early,
// and closes the connection
func (resp *response) writeHijacked(status int, contentType string, writeBody func(io.Writer) error) error {
	defer resp.close()

	w := bufio.NewWriter(&stallWriter{w: resp.conn, setDeadline: resp.conn.SetWriteDeadline, transfer: resp.transfer})
	_, err := fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: %s\r\nX-Content-Type-Options: nosniff\r\n", status, http.StatusText(status), contentType)
	if err != nil {
		return err
	}
	if resp.requestID != "" {
		_, err = fmt.Fprintf(w, "%s: %s\r\n", requestIDHeader, resp.requestID)
		if err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
	if err != nil {
		return err
	}
	body := httputil.NewChunkedWriter(w)
	err = writeBody(body)
	if err != nil {
		return err
	}
	// The last chunk followed by the empty trailer
	err = body.Close()
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "\r\n")
	if err != nil {
		return err
	}
	return w.Flush()
}

// transferLimits limits how long a transfer may stall
// and, after a grace period of timeout, its average rate
type transferLimits struct {
	timeout time.Duration // Zero or negative means no limits
	minRate int64         // Bytes per second, zero or negative means no rate limit
	start   time.Time
	done    int64
}

// deadline returns the deadline for transferring n more bytes,
// or the zero time if there is no limit
func (t *transferLimits) deadline(n int) time.Time {
	if t.timeout <= 0 {
		return time.Time{}
	}
	now := time.Now()
	if t.start.IsZero() {
		t.start = now
	}
	deadline := now.Add(t.timeout)
	if t.minRate > 0 {
		atMinRate := time.Duration(float64(t.done+int64(n)) / float64(t.minRate) * float64(time.Second))
		if rateDeadline := t.start.Add(t.timeout + atMinRate); rateDeadline.Before(deadline) {
			deadline = rateDeadline
		}
	}
	return deadline
}

// stallReader sets a read deadline before every read,
// so that a client that stops sending, or sends slower
// than the minimum rate, ends the request
type stallReader struct {
	r           io.Reader
	setDeadline func(time.Time) error
	transfer    transferLimits
}

func (s *stallReader) Read(p []byte) (int, error) {
	if s.transfer.timeout > 0 {
		_ = s.setDeadline(s.transfer.deadline(1))
	}
	n, err := s.r.Read(p)
	s.transfer.done += int64(n)
	return n, err
}

// stallWriteChunkSize is the size of the chunks that stallWriter
// writes with a new deadline, so that the deadline applies to
// progress and not to a whole large write
const stallWriteChunkSize = 64 << 10

// stallWriter sets a write deadline before every chunk written,
// so that a client that stops receiving, or receives slower
// than the minimum rate, ends the request
type stallWriter struct {
	w           io.Writer
	setDeadline func(time.Time) error
	transfer    transferLimits
}

func (s *stallWriter) Write(p []byte) (written int, err error) {
	for len(p) > 0 {
		chunk := p[:min(len(p), stallWriteChunkSize)]
		if s.transfer.timeout > 0 {
			_ = s.setDeadline(s.transfer.deadline(len(chunk)))
		}
		n, err := s.w.Write(chunk)
		written += n
		s.transfer.done += int64(n)
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}
