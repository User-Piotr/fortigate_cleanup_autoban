package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func testAPIClient(t *testing.T, server *httptest.Server) *apiClient {
	t.Helper()

	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatalf("parse test server address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	return &apiClient{
		http:  server.Client(),
		host:  host,
		port:  port,
		token: "test-token",
	}
}

func TestCfgSaveMode(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
		want       string
		wantErr    string
	}{
		{
			name:       "object result",
			statusCode: http.StatusOK,
			response:   `{"results":{"cfg-save":"manual"}}`,
			want:       "manual",
		},
		{
			name:       "array result",
			statusCode: http.StatusOK,
			response:   `{"results":[{"cfg-save":"REVERT"}]}`,
			want:       "revert",
		},
		{
			name:       "missing cfg-save",
			statusCode: http.StatusOK,
			response:   `{"results":{}}`,
			wantErr:    "cfg-save is missing",
		},
		{
			name:       "malformed response",
			statusCode: http.StatusOK,
			response:   `{`,
			wantErr:    "parse system/global response",
		},
		{
			name:       "API failure",
			statusCode: http.StatusForbidden,
			response:   `{"status":"error","path":"system","name":"global"}`,
			wantErr:    "read cfg-save mode: HTTP 403 on system/global",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %q, want %q", r.Method, http.MethodGet)
				}
				if r.URL.Path != "/api/v2/cmdb/system/global" {
					t.Errorf("path = %q, want %q", r.URL.Path, "/api/v2/cmdb/system/global")
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := testAPIClient(t, server)
			got, err := client.cfgSaveMode()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("cfgSaveMode() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("cfgSaveMode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("cfgSaveMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveConfigIfNeeded(t *testing.T) {
	tests := []struct {
		mode         string
		wantSaved    string
		wantRequests int
	}{
		{mode: "automatic", wantRequests: 1},
		{mode: "manual", wantSaved: "manual", wantRequests: 2},
		{mode: "revert", wantSaved: "revert", wantRequests: 2},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			requestCount := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
				}

				switch r.URL.Path {
				case "/api/v2/cmdb/system/global":
					if r.Method != http.MethodGet {
						t.Errorf("cfg-save method = %q, want %q", r.Method, http.MethodGet)
					}
					_, _ = w.Write([]byte(fmt.Sprintf(`{"results":{"cfg-save":%q}}`, tt.mode)))
				case "/api/v2/monitor/system/config/save":
					if r.Method != http.MethodPost {
						t.Errorf("save method = %q, want %q", r.Method, http.MethodPost)
					}
					if got := r.URL.Query().Get("vdom"); got != "root" {
						t.Errorf("vdom = %q, want %q", got, "root")
					}
					if got := r.Header.Get("Content-Type"); got != "application/json" {
						t.Errorf("Content-Type = %q, want %q", got, "application/json")
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read request body: %v", err)
					} else if got := string(body); got != `{}` {
						t.Errorf("body = %q, want %q", got, `{}`)
					}
				default:
					t.Errorf("unexpected path %q", r.URL.Path)
				}
			}))
			defer server.Close()

			client := testAPIClient(t, server)
			got, err := client.saveConfigIfNeeded()
			if err != nil {
				t.Fatalf("saveConfigIfNeeded() error = %v", err)
			}
			if got != tt.wantSaved {
				t.Fatalf("saveConfigIfNeeded() = %q, want %q", got, tt.wantSaved)
			}
			if requestCount != tt.wantRequests {
				t.Fatalf("request count = %d, want %d", requestCount, tt.wantRequests)
			}
		})
	}

	t.Run("unknown mode fails", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"results":{"cfg-save":"unexpected"}}`))
		}))
		defer server.Close()

		client := testAPIClient(t, server)
		_, err := client.saveConfigIfNeeded()
		if err == nil || !strings.Contains(err.Error(), `unsupported cfg-save mode "unexpected"`) {
			t.Fatalf("saveConfigIfNeeded() error = %v, want unsupported mode", err)
		}
	})

	t.Run("save API failure", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/cmdb/system/global" {
				_, _ = w.Write([]byte(`{"results":{"cfg-save":"manual"}}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error","path":"system/config","name":"save"}`))
		}))
		defer server.Close()

		client := testAPIClient(t, server)
		_, err := client.saveConfigIfNeeded()
		if err == nil || !strings.Contains(err.Error(), "save configuration: HTTP 500 on system/config/save") {
			t.Fatalf("saveConfigIfNeeded() error = %v, want API failure", err)
		}
	})
}
