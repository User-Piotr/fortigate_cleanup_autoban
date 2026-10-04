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
		allExpired    bool
		ignoreUpdate  bool
		delayUpdate   time.Duration
		delaySave     time.Duration
		failVerify    bool
		verifyBody    string
		failRequest   string
		failModeRead  int
		wantExit      int
		wantAfterRead []string
		wantOutput    []string
	}{
		{name: "flag disabled", wantAfterRead: []string{update, readGroup, deleteOld}, wantOutput: []string{"[DELETED]", "SUMMARY"}},
		{name: "dry run", flags: []string{"-save-if-needed", "-dry-run"}, modes: []string{"manual"}, wantAfterRead: []string{readMode}, wantOutput: []string{"DRY RUN", "cfg-save:", "manual"}},
		{name: "save with dry run", flags: []string{"-save", "-dry-run"}, modes: []string{"revert"}, wantAfterRead: []string{readMode}, wantOutput: []string{"DRY RUN", "cfg-save:", "revert"}},
		{name: "save after dry run", flags: []string{"-dry-run", "-save"}, modes: []string{"manual"}, wantAfterRead: []string{readMode}, wantOutput: []string{"DRY RUN", "cfg-save:", "manual"}},
		{name: "dry run without save flag", flags: []string{"-dry-run"}, modes: []string{"manual"}, wantAfterRead: []string{readMode}, wantOutput: []string{"DRY RUN", "cfg-save:", "manual"}},
		{name: "dry run automatic", flags: []string{"-dry-run"}, modes: []string{"automatic"}, wantAfterRead: []string{readMode}, wantOutput: []string{"cfg-save:", "automatic"}},
		{name: "dry run revert", flags: []string{"-save-if-needed", "-dry-run"}, modes: []string{"revert"}, wantAfterRead: []string{readMode}, wantOutput: []string{"cfg-save:", "revert"}},
		{name: "dry run no expired entries", flags: []string{"-dry-run"}, noExpired: true, modes: []string{"manual"}, wantAfterRead: []string{readMode}, wantOutput: []string{"Nothing to expire", "cfg-save:", "manual"}},
		{name: "dry run mode read fails", flags: []string{"-dry-run"}, failModeRead: 1, wantAfterRead: []string{readMode}, wantOutput: []string{"[WARN]", "SUMMARY", "cfg-save:", "unknown"}},
		{name: "dry run unsupported mode", flags: []string{"-save-if-needed", "-dry-run"}, modes: []string{"unexpected"}, wantAfterRead: []string{readMode}, wantOutput: []string{"[WARN]", "unsupported cfg-save mode", "unknown"}},
		{name: "nothing expired", flags: []string{"-save-if-needed"}, noExpired: true, wantOutput: []string{"not needed"}},
		{name: "automatic", flags: []string{"-save-if-needed"}, modes: []string{"automatic", "automatic"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode}, wantOutput: []string{"[DELETED]", "automatic"}},
		{name: "manual", flags: []string{"-save-if-needed"}, modes: []string{"manual", "manual"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"[SAVED]", "saved (manual)"}},
		{name: "save manual", flags: []string{"-save"}, modes: []string{"manual", "manual"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"[SAVED]", "saved (manual)"}},
		{name: "save revert", flags: []string{"-save"}, modes: []string{"revert", "revert"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"[SAVED]", "saved (revert)"}},
		{name: "save automatic", flags: []string{"-save"}, modes: []string{"automatic", "automatic"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode}, wantOutput: []string{"automatic"}},
		{name: "revert", flags: []string{"-save-if-needed"}, modes: []string{"revert", "revert"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"[SAVED]", "saved (revert)"}},
		{name: "preflight read fails", flags: []string{"-save-if-needed"}, failModeRead: 1, wantExit: 1, wantAfterRead: []string{readMode}, wantOutput: []string{"configuration save preflight"}},
		{name: "unsupported preflight mode", flags: []string{"-save-if-needed"}, modes: []string{"unexpected"}, wantExit: 1, wantAfterRead: []string{readMode}, wantOutput: []string{"unsupported cfg-save mode"}},
		{name: "group update fails", flags: []string{"-save-if-needed"}, modes: []string{"manual"}, failRequest: update, wantExit: 1, wantAfterRead: []string{readMode, update}, wantOutput: []string{"failed to update group membership"}},
		{name: "delete fails", flags: []string{"-save-if-needed"}, modes: []string{"manual"}, failRequest: deleteOld, wantExit: 1, wantAfterRead: []string{readMode, update, readGroup, deleteOld}, wantOutput: []string{"SUMMARY", "skipped (delete failures)"}},
		{name: "final read fails", flags: []string{"-save-if-needed"}, modes: []string{"manual"}, failModeRead: 2, wantExit: 1, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode}, wantOutput: []string{"[DELETED]", "SUMMARY", "could not be persisted", "read cfg-save mode"}},
		{name: "save fails", flags: []string{"-save-if-needed"}, modes: []string{"manual", "manual"}, failRequest: save, wantExit: 1, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"[DELETED]", "SUMMARY", "could not be persisted", "save configuration"}},
		{name: "mode changes to automatic", flags: []string{"-save-if-needed"}, modes: []string{"manual", "automatic"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode}, wantOutput: []string{"automatic"}},
		{name: "mode changes to revert", flags: []string{"-save-if-needed"}, modes: []string{"automatic", "revert"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"saved (revert)"}},
		{name: "entire group expires", flags: []string{"-save"}, allExpired: true, modes: []string{"manual", "manual"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"[DELETED]", "saved (manual)"}},
		{name: "PUT does not remove members", flags: []string{"-save"}, allExpired: true, ignoreUpdate: true, modes: []string{"manual"}, wantExit: 1, wantAfterRead: []string{readMode, update, readGroup}, wantOutput: []string{"membership verification failed", "still in group", "No address deletions or explicit configuration save"}},
		{name: "PUT times out after update", flags: []string{"-save", "-write-timeout", "50ms"}, allExpired: true, delayUpdate: 200 * time.Millisecond, modes: []string{"manual", "manual"}, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"Group PUT timed out", "read-back confirms", "[DELETED]", "[SAVED]"}},
		{name: "PUT times out without update", flags: []string{"-save", "-write-timeout", "50ms"}, allExpired: true, ignoreUpdate: true, delayUpdate: 200 * time.Millisecond, modes: []string{"manual"}, wantExit: 1, wantAfterRead: []string{readMode, update, readGroup}, wantOutput: []string{"PUT timed out", "still in group", "No address deletions or explicit configuration save"}},
		{name: "explicit save times out", flags: []string{"-save", "-write-timeout", "50ms"}, modes: []string{"manual", "manual"}, delaySave: 200 * time.Millisecond, wantExit: 1, wantAfterRead: []string{readMode, update, readGroup, deleteOld, readMode, save}, wantOutput: []string{"[DELETED]", "save outcome is unknown", "config save:", "outcome unknown", "context deadline exceeded"}},
		{name: "verification read fails", flags: []string{"-save"}, failVerify: true, modes: []string{"manual"}, wantExit: 1, wantAfterRead: []string{readMode, update, readGroup}, wantOutput: []string{"membership verification failed", "HTTP 403", "No address deletions"}},
		{name: "verification JSON invalid", verifyBody: `{`, wantExit: 1, wantAfterRead: []string{update, readGroup}, wantOutput: []string{"parse group after update", "No address deletions"}},
		{name: "verification results missing", verifyBody: `{"results":[]}`, wantExit: 1, wantAfterRead: []string{update, readGroup}, wantOutput: []string{"expected one group after update", "No address deletions"}},
		{name: "verification members missing", verifyBody: `{"results":[{}]}`, wantExit: 1, wantAfterRead: []string{update, readGroup}, wantOutput: []string{"group member list is missing or null", "No address deletions"}},
		{name: "verification retained member missing", verifyBody: `{"results":[{"member":[]}]}`, wantExit: 1, wantAfterRead: []string{update, readGroup}, wantOutput: []string{"retained address is missing from group", "No address deletions"}},
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
			groupReads := 0
			groupMembers := []groupMember{{Name: "old"}, {Name: "keep"}}
			if tt.allExpired {
				groupMembers = []groupMember{{Name: "old"}}
			}
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
				if request == readGroup {
					groupReads++
				}
				if request == tt.failRequest || (request == readMode && modeReads == tt.failModeRead) || (request == readGroup && groupReads == 2 && tt.failVerify) {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"status":"error"}`))
					return
				}
				switch request {
				case readGroup:
					if groupReads == 2 && tt.verifyBody != "" {
						_, _ = w.Write([]byte(tt.verifyBody))
						return
					}
					_ = json.NewEncoder(w).Encode(addrgrpListResponse{Results: []addrgrpObject{{Member: groupMembers}}})
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
					}
					wantMembers := []groupMember{{Name: "keep"}}
					if tt.allExpired {
						wantMembers = []groupMember{}
					}
					if !reflect.DeepEqual(body.Member, wantMembers) {
						t.Errorf("updated members = %#v, want %#v (an empty group must use [], not null)", body.Member, wantMembers)
					}
					if !tt.ignoreUpdate {
						groupMembers = body.Member
					}
					if tt.delayUpdate > 0 {
						time.Sleep(tt.delayUpdate)
					}
				case save:
					if tt.delaySave > 0 {
						time.Sleep(tt.delaySave)
					}
					w.WriteHeader(http.StatusOK)
				case deleteOld:
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
			wantRequests := []string{readGroup, readOld}
			if !tt.allExpired {
				wantRequests = append(wantRequests, readKeep)
			}
			wantRequests = append(wantRequests, tt.wantAfterRead...)
			if !reflect.DeepEqual(gotRequests, wantRequests) {
				t.Errorf("requests = %v, want %v", gotRequests, wantRequests)
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(string(output), want) {
					t.Errorf("output missing %q:\n%s", want, output)
				}
			}
			for _, arg := range tt.flags {
				if arg == "-dry-run" && strings.Contains(string(output), "config save:") {
					t.Errorf("dry-run output still includes config save row:\n%s", output)
				}
			}
			wantSaved := tt.wantExit == 0 && len(tt.wantAfterRead) > 0 && tt.wantAfterRead[len(tt.wantAfterRead)-1] == save
			if gotSaved := strings.Contains(string(output), "[SAVED]"); gotSaved != wantSaved {
				t.Errorf("[SAVED] present = %t, want %t:\n%s", gotSaved, wantSaved, output)
			}
		})
	}
}
