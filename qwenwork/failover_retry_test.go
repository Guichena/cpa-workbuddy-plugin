package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPickNextAuth_NoCurrent(t *testing.T) {
	// pickNextAuth always filters out its currentAuthID arg, so passing
	// the empty string sweeps the whole list — and we have no host RPC
	// available here, so the call must return ok=false without panicking.
	if _, _, ok := pickNextAuth(""); ok {
		t.Fatal("pickNextAuth must return ok=false when host RPC is unavailable")
	}
}

func TestReadAllUpstreamErr(t *testing.T) {
	cases := []struct {
		name string
		in   io.Reader
		want string
	}{
		{"nil reader", nil, ""},
		{"error reader", &errReader{}, ""},
		{"empty body", strings.NewReader(""), ""},
		{"plain text", strings.NewReader("Method Not Allowed"), "Method Not Allowed"},
		{"json body", strings.NewReader(`{"error":"forbidden"}`), `{"error":"forbidden"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readAllUpstreamErr(tc.in); got != tc.want {
				t.Fatalf("readAllUpstreamErr(%v) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestRebuildRequestWithQwenAuth_NilAuth(t *testing.T) {
	if _, err := rebuildRequestWithQwenAuth(nil, []byte("body"), ""); err == nil {
		t.Fatal("rebuildRequestWithQwenAuth(nil, ...) must return an error")
	}
}

type errReader struct{}

func (e *errReader) Read(_ []byte) (int, error) { return 0, errors.New("simulated read error") }
