package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestSummarizeFortiGateError(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "used address", body: `{"status":"error","error":-23,"path":"firewall","name":"address","mkey":"192.0.2.1"}`, want: "HTTP 500 on firewall/address/192.0.2.1 (error=-23)"},
		{name: "message", body: `{"status":"error","error":-14,"message":"Permission denied"}`, want: "HTTP 500 (error=-14): Permission denied"},
		{name: "CLI error", body: `{"status":"error","error":-23,"cli_error":"Entry is used.\n"}`, want: "HTTP 500 (error=-23): Entry is used."},
		{name: "CLI error list", body: `{"status":"error","cli_error":["Entry is used.","Return code -23"]}`, want: "HTTP 500: Entry is used.; Return code -23"},
		{name: "no error details", body: `{"status":"error","path":"firewall","name":"address"}`, want: "HTTP 500 on firewall/address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeError(http.StatusInternalServerError, []byte(tt.body), nil); got != tt.want {
				t.Fatalf("summarizeError() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadToken(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		fromStdin bool
		stdin     string
		want      string
		wantErr   string
	}{
		{name: "argument token", token: "argument-token", want: "argument-token"},
		{name: "stdin token", fromStdin: true, stdin: "stdin-token\n", want: "stdin-token"},
		{name: "both sources", token: "argument-token", fromStdin: true, stdin: "stdin-token", wantErr: "either -token or -token-stdin"},
		{name: "empty stdin", fromStdin: true, stdin: " \n", wantErr: "standard input is empty"},
		{name: "oversized stdin", fromStdin: true, stdin: strings.Repeat("x", maxTokenBytes+1), wantErr: "exceeds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readToken(tt.token, tt.fromStdin, strings.NewReader(tt.stdin))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("readToken() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readToken() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("readToken() = %q, want %q", got, tt.want)
			}
		})
	}
}
