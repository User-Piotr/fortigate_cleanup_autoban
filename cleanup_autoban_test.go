package main

import (
	"strings"
	"testing"
)

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
