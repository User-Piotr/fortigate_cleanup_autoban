package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// A subprocess lets the integration tests exercise main's real exit codes.
func TestCleanupMainProcess(t *testing.T) {
	if os.Getenv("CLEANUP_AUTOBAN_TEST_MAIN") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
			main()
			os.Exit(0)
		}
	}
	t.Fatal("missing CLI argument separator")
}

func TestCleanupConfigSaving(t *testing.T) {
	const (
		readGroup = "GET /api/v2/cmdb/firewall/addrgrp/admin-failed-login"
		readOld   = "GET /api/v2/cmdb/firewall/address/old"
		readKeep  = "GET /api/v2/cmdb/firewall/address/keep"
		readMode  = "GET /api/v2/cmdb/system/global"
		update    = "PUT /api/v2/cmdb/firewall/addrgrp/admin-failed-login"
		deleteOld = "DELETE /api/v2/cmdb/firewall/address/old"
		save      = "POST /api/v2/monitor/system/config/save?vdom=root"
	)
	tests := []struct {
		name          string
		flags         []string
		modes         []string
		noExpired     bool
		failRequest   string
		failModeRead  int
		wantExit      int
		wantAfterRead []string
		wantOutput    []string
	}{
		{name: "flag disabled", wantAfterRead: []string{update, deleteOld}, wantOutput: []string{"[DELETED]", "SUMMARY"}},
		{name: "dry run", flags: []string{"-save-if-needed", "-dry-run"}, wantOutput: []string{"DRY RUN", "disabled in dry run"}},
		{name: "nothing expired", flags: []string{"-save-if-needed"}, noExpired: true, wantOutput: []string{"not needed"}},
		{name: "automatic", flags: []string{"-save-if-needed"}, modes: []string{"automatic", "automatic"}, wantAfterRead: []string{readMode, update, deleteOld, readMode}, wantOutput: []string{"[DELETED]", "automatic"}},
		{name: "manual", flags: []string{"-save-if-needed"}, modes: []string{"manual", "manual"}, wantAfterRead: []string{readMode, update, deleteOld, readMode, save}, wantOutput: []string{"[SAVED]", "saved (manual)"}},
		{name: "revert", flags: []string{"-save-if-needed"}, modes: []string{"revert", "revert"}, wantAfterRead: []string{readMode, update, deleteOld, readMode, save}, wantOutput: []string{"[SAVED]", "saved (revert)"}},
		{name: "preflight read fails", flags: []string{"-save-if-needed"}, failModeRead: 1, wantExit: 1, wantAfterRead: []string{readMode}, wantOutput: []string{"configuration save preflight"}},
		{name: "unsupported preflight mode", flags: []string{"-save-if-needed"}, modes: []string{"unexpected"}, wantExit: 1, wantAfterRead: []string{readMode}, wantOutput: []string{"unsupported cfg-save mode"}},
		{name: "group update fails", flags: []string{"-save-if-needed"}, modes: []string{"manual"}, failRequest: update, wantExit: 1, wantAfterRead: []string{readMode, update}, wantOutput: []string{"failed to update group membership"}},
		{name: "delete fails", flags: []string{"-save-if-needed"}, modes: []string{"manual"}, failRequest: deleteOld, wantExit: 1, wantAfterRead: []string{readMode, update, deleteOld}, wantOutput: []string{"SUMMARY", "skipped (delete failures)"}},
		{name: "final read fails", flags: []string{"-save-if-needed"}, modes: []string{"manual"}, failModeRead: 2, wantExit: 1, wantAfterRead: []string{readMode, update, deleteOld, readMode}, wantOutput: []string{"[DELETED]", "SUMMARY", "could not be persisted", "read cfg-save mode"}},
		{name: "save fails", flags: []string{"-save-if-needed"}, modes: []string{"manual", "manual"}, failRequest: save, wantExit: 1, wantAfterRead: []string{readMode, update, deleteOld, readMode, save}, wantOutput: []string{"[DELETED]", "SUMMARY", "could not be persisted", "save configuration"}},
		{name: "mode changes to automatic", flags: []string{"-save-if-needed"}, modes: []string{"manual", "automatic"}, wantAfterRead: []string{readMode, update, deleteOld, readMode}, wantOutput: []string{"automatic"}},
		{name: "mode changes to revert", flags: []string{"-save-if-needed"}, modes: []string{"automatic", "revert"}, wantAfterRead: []string{readMode, update, deleteOld, readMode, save}, wantOutput: []string{"saved (revert)"}},
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			modeReads := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				request := r.Method + " " + r.URL.RequestURI()
				requests = append(requests, request)
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing test authorization")
				}
				if request == readMode {
					modeReads++
				}
				if request == tt.failRequest || (request == readMode && modeReads == tt.failModeRead) {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"status":"error"}`))
					return
				}
				switch request {
				case readGroup:
					_, _ = w.Write([]byte(`{"results":[{"member":[{"name":"old"},{"name":"keep"}]}]}`))
				case readOld:
					comment := "autoban:1"
					if tt.noExpired {
						comment = "manual block"
					}
					_, _ = fmt.Fprintf(w, `{"results":[{"comment":%q}]}`, comment)
				case readKeep:
					_, _ = w.Write([]byte(`{"results":[{"comment":"manual block"}]}`))
				case readMode:
					if modeReads > len(tt.modes) {
						t.Error("unexpected cfg-save read")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = fmt.Fprintf(w, `{"results":{"cfg-save":%q}}`, tt.modes[modeReads-1])
				case update:
					var body struct {
						Member []groupMember `json:"member"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode group update: %v", err)
					} else if !reflect.DeepEqual(body.Member, []groupMember{{Name: "keep"}}) {
						t.Errorf("group update discarded manual member: %+v", body.Member)
					}
				case deleteOld, save:
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected request %q", request)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := testAPIClient(t, server)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			args := []string{"-test.run=^TestCleanupMainProcess$", "--", "-host", client.host, "-port", fmt.Sprint(client.port), "-token-stdin", "-days", "7"}
			cmd := exec.CommandContext(ctx, executable, append(args, tt.flags...)...)
			cmd.Env = append(os.Environ(), "CLEANUP_AUTOBAN_TEST_MAIN=1")
			cmd.Stdin = strings.NewReader("test-token\n")
			output, runErr := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("CLI timed out: %s", output)
			}
			if cmd.ProcessState == nil {
				t.Fatalf("start CLI: %v", runErr)
			}
			if got := cmd.ProcessState.ExitCode(); got != tt.wantExit {
				t.Fatalf("exit = %d, want %d (error %v):\n%s", got, tt.wantExit, runErr, output)
			}
			mu.Lock()
			gotRequests := append([]string(nil), requests...)
			mu.Unlock()
			wantRequests := append([]string{readGroup, readOld, readKeep}, tt.wantAfterRead...)
			if !reflect.DeepEqual(gotRequests, wantRequests) {
				t.Errorf("requests = %v, want %v", gotRequests, wantRequests)
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(string(output), want) {
					t.Errorf("output missing %q:\n%s", want, output)
				}
			}
			wantSaved := tt.wantExit == 0 && len(tt.wantAfterRead) > 0 && tt.wantAfterRead[len(tt.wantAfterRead)-1] == save
			if gotSaved := strings.Contains(string(output), "[SAVED]"); gotSaved != wantSaved {
				t.Errorf("[SAVED] present = %t, want %t:\n%s", gotSaved, wantSaved, output)
			}
		})
	}
}
