package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Conn represents an ACP connection over a pair of streams (usually stdin/stdout).
type Conn struct {
	reader  *bufio.Scanner
	writer  io.Writer
	writeMu sync.Mutex
}

func NewConn(r io.Reader, w io.Writer) *Conn {
	scanner := bufio.NewScanner(r)
	// ACP messages are one per line.
	// Increase the buffer to 1MB to handle large context payloads.
	const maxScanBuffer = 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxScanBuffer)

	return &Conn{
		reader: scanner,
		writer: w,
	}
}

// ReadMessage reads the next JSON-RPC message from the connection.
func (c *Conn) ReadMessage() ([]byte, error) {
	if !c.reader.Scan() {
		if err := c.reader.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return c.reader.Bytes(), nil
}

// SendResponse sends a JSON-RPC response.
func (c *Conn) SendResponse(id any, result any) error {
	res := Response{
		JSONRPC: "2.0",
		ID:      id,
	}
	if result != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		res.Result = raw
	}
	return c.write(res)
}

// SendError sends a JSON-RPC error response.
func (c *Conn) SendError(id any, code int, message string) error {
	res := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &Error{
			Code:    code,
			Message: message,
		},
	}
	return c.write(res)
}

// SendNotification sends a JSON-RPC notification.
func (c *Conn) SendNotification(method string, params any) error {
	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		notif.Params = raw
	}
	return c.write(notif)
}

func (c *Conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = fmt.Fprintf(c.writer, "%s\n", string(b))
	return err
}
