package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Client is a small ACP client that speaks newline-delimited JSON-RPC over
// a stream pair and exposes typed request/response methods.
type Client struct {
	conn          *Conn
	notifications func(Notification)
	nextID        int

	pendingMu sync.Mutex
	pending   map[string]chan responseMessage
}

type responseMessage struct {
	Result json.RawMessage
	Error  *Error
	reqErr error
}

type rawResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

type rpcNotify struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// NewClient creates an ACP client bound to the provided transport.
func NewClient(r io.Reader, w io.Writer, onNotification func(Notification)) *Client {
	c := &Client{
		conn:          NewConn(r, w),
		notifications: onNotification,
		pending:       make(map[string]chan responseMessage),
	}
	return c
}

// StartLaunch starts the background reader loop.
func (c *Client) StartLaunch() {
	go c.readLoop()
}

// Call invokes an ACP method and decodes the result into out when provided.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if c == nil {
		return errors.New("nil acp client")
	}

	c.pendingMu.Lock()
	id := c.nextCallID()
	idBuf := make(chan responseMessage, 1)
	c.pending[id] = idBuf
	c.pendingMu.Unlock()

	req := Request{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			c.clearPending(id)
			return err
		}
		req.Params = raw
	}

	if err := c.conn.Send(req); err != nil {
		c.clearPending(id)
		return err
	}

	select {
	case <-ctx.Done():
		c.clearPending(id)
		return ctx.Err()
	case res := <-idBuf:
		if res.reqErr != nil {
			return res.reqErr
		}
		if res.Error != nil {
			msg := strings.TrimSpace(res.Error.Message)
			if msg == "" {
				msg = "ACP request failed"
			}
			return fmt.Errorf("%d: %s", res.Error.Code, msg)
		}
		if out == nil {
			return nil
		}
		if len(res.Result) == 0 {
			return nil
		}
		return json.Unmarshal(res.Result, out)
	}
}

func (c *Client) clearPending(id string) {
	c.pendingMu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.pendingMu.Unlock()
	if ok {
		select {
		case <-ch:
		default:
		}
	}
}

func (c *Client) nextCallID() string {
	c.nextID++
	return strconv.FormatInt(int64(c.nextID), 10)
}

func (c *Client) readLoop() {
	for {
		raw, err := c.conn.ReadMessage()
		if err != nil {
			c.closePending(errors.New("acp connection closed"))
			if c.notifications != nil {
				c.notifications(Notification{
					JSONRPC: JSONRPCVersion,
					Method:  MethodConnectionClosed,
					Params:  nil,
				})
			}
			return
		}
		if len(raw) == 0 {
			continue
		}

		var base rpcNotify
		if err := json.Unmarshal(raw, &base); err != nil {
			continue
		}

		if len(base.ID) > 0 {
			var res rawResponse
			if err := json.Unmarshal(raw, &res); err != nil {
				continue
			}
			id := decodeRPCID(res.ID)
			if id == "" {
				continue
			}
			c.pendingMu.Lock()
			ch, ok := c.pending[id]
			delete(c.pending, id)
			c.pendingMu.Unlock()
			if ok {
				select {
				case ch <- responseMessage{Result: res.Result, Error: res.Error}:
				default:
				}
			}
			continue
		}

		if c.notifications == nil {
			continue
		}

		c.notifications(Notification{
			JSONRPC: base.JSONRPC,
			Method:  base.Method,
			Params:  base.Params,
		})
	}
}

func (c *Client) closePending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		select {
		case ch <- responseMessage{reqErr: err}:
		default:
		}
	}
}

func decodeRPCID(raw json.RawMessage) string {
	var sid string
	if raw == nil {
		return ""
	}
	if err := json.Unmarshal(raw, &sid); err == nil {
		return sid
	}
	var iid int64
	if err := json.Unmarshal(raw, &iid); err == nil {
		return strconv.FormatInt(iid, 10)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatInt(int64(f), 10)
	}
	return ""
}
