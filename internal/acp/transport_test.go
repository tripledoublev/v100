package acp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestConnReadMessageAndWritesJSONRPC(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1}` + "\n")
	var out bytes.Buffer
	conn := NewConn(in, &out)

	msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage returned error: %v", err)
	}
	if !strings.Contains(string(msg), MethodInitialize) {
		t.Fatalf("ReadMessage = %s", msg)
	}
	if _, err := conn.ReadMessage(); err == nil {
		t.Fatal("ReadMessage returned nil error at EOF")
	}

	if err := conn.SendResponse(1, InitializeResult{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatalf("SendResponse returned error: %v", err)
	}
	if err := conn.SendError(2, ErrSessionNotFound, ErrorMessage(ErrSessionNotFound)); err != nil {
		t.Fatalf("SendError returned error: %v", err)
	}
	if err := conn.SendNotification(MethodSessionUpdate, SessionUpdateParams{SessionID: "s1"}); err != nil {
		t.Fatalf("SendNotification returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3:\n%s", len(lines), out.String())
	}
	var res Response
	if err := json.Unmarshal([]byte(lines[0]), &res); err != nil {
		t.Fatalf("response unmarshal: %v", err)
	}
	if res.JSONRPC != JSONRPCVersion || len(res.Result) == 0 || res.ID == nil {
		t.Fatalf("response = %#v", res)
	}
	var errRes Response
	if err := json.Unmarshal([]byte(lines[1]), &errRes); err != nil {
		t.Fatalf("error response unmarshal: %v", err)
	}
	if errRes.Error == nil || errRes.Error.Code != ErrSessionNotFound {
		t.Fatalf("error response = %#v", errRes)
	}
	var notif Notification
	if err := json.Unmarshal([]byte(lines[2]), &notif); err != nil {
		t.Fatalf("notification unmarshal: %v", err)
	}
	if notif.JSONRPC != JSONRPCVersion || notif.Method != MethodSessionUpdate {
		t.Fatalf("notification = %#v", notif)
	}
}
