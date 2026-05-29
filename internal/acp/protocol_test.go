package acp

import "testing"

func TestErrorMessageCoversACPErrorCodes(t *testing.T) {
	cases := map[int]string{
		ErrParse:                      "parse error",
		ErrInvalidRequest:             "invalid request",
		ErrMethodNotFound:             "method not found",
		ErrInvalidParams:              "invalid params",
		ErrInternal:                   "internal error",
		ErrSessionNotFound:            "session not found",
		ErrSessionAlreadyExists:       "session already exists",
		ErrSessionBusy:                "session busy",
		ErrSessionClosing:             "session closing",
		ErrUnsupportedProtocolVersion: "unsupported protocol version",
		ErrProviderConfiguration:      "provider configuration error",
		1234:                          "unknown error",
	}
	for code, want := range cases {
		if got := ErrorMessage(code); got != want {
			t.Fatalf("ErrorMessage(%d) = %q, want %q", code, got, want)
		}
	}
}
