// cleanup_autoban.go
//
// Age-based expiry for the FortiGate "admin-failed-login" address group.
//
// Replaces the weekly `unset member` reset stitch entirely. Instead of
// wiping the whole group on a fixed schedule (which also nukes an IP
// blocked 5 minutes earlier, and any manually-curated entries mixed into
// the same group), this walks each member individually and only expires
// entries older than -days, based on the nanosecond-epoch timestamp the
// automation action stamps into the comment field:
//
//	set comment autoban:%%log.eventtime%%
//
// For every current member of -group:
//   - comment matches "autoban:<digits>" AND older than -days
//     -> remove from group, then delete the address object (purge)
//   - comment does NOT match the prefix (manual/curated entry,
//     e.g. AS200730_Block, Manual_Block_45.134.212.70)
//     -> always left alone, never touched, regardless of age
//   - comment matches but still within -days
//     -> left alone, still doing its job
//
// Build (from a machine with Go installed). Always pass -o: without it,
// `go build` names the binary after the source file and overwrites it.
//
//	go get github.com/charmbracelet/lipgloss@latest
//
//	Windows binary, built from any OS:
//	  GOOS=windows GOARCH=amd64 go build -o expire_autoban.exe .
//
//	Native build:
//	  go build -o expire_autoban .
//
// Usage:
//
//	expire_autoban.exe -host 172.20.20.154 -port 443 -token <api_token> -days 7 -dry-run
//	expire_autoban.exe -host 172.20.20.154 -port 443 -token <api_token> -days 7
//
// Intended to run on its own schedule (e.g. daily via Windows Task
// Scheduler) -- it no longer depends on a separate weekly reset stitch.
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	secondsPerDay = 86400
	maxTokenBytes = 64 * 1024
)

var commentPrefixRe = regexp.MustCompile(`^autoban:(\d+)$`)

var (
	colorGreen  = lipgloss.Color("42")
	colorRed    = lipgloss.Color("204")
	colorYellow = lipgloss.Color("214")
	colorGray   = lipgloss.Color("245")
	colorBlue   = lipgloss.Color("39")
	colorOrange = lipgloss.Color("208")

	keepStyle   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	expireStyle = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	warnStyle   = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	failStyle   = lipgloss.NewStyle().Foreground(colorRed)
	okStyle     = lipgloss.NewStyle().Foreground(colorGreen)
	dimStyle    = lipgloss.NewStyle().Foreground(colorGray)
	headerStyle = lipgloss.NewStyle().Foreground(colorGray).Bold(true)

	bannerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Background(colorBlue).
			Padding(0, 1)

	summaryBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(0, 2)

	tableBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(0, 1)

	labelStyle = lipgloss.NewStyle().Foreground(colorGray).Width(12)

	titleColorStyle = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
	boxBorderStyle  = lipgloss.NewStyle().Foreground(colorBlue)

	dryRunTagStyle = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
)

// out accumulates all "pretty" output for the run. Everything gets wrapped
// in one outer bordered box at the end, so nothing can print directly to
// stdout mid-run -- error paths that os.Exit still go straight to stderr,
// since a half-finished box would look broken.
var out strings.Builder

func dryRunNote(format string, a ...any) {
	fmt.Fprintf(&out, "%s %s\n", dryRunTagStyle.Render("[DRY RUN]"), fmt.Sprintf(format, a...))
}

// kvBox renders alternating label/value strings as one aligned "label: value"
// per line, inside a rounded box. Both PARAMETERS and SUMMARY are just
// label/value lists, so they share this instead of hand-formatting each.
func kvBox(pairs ...string) string {
	lines := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		lines = append(lines, labelStyle.Render(pairs[i])+" "+pairs[i+1])
	}
	return summaryBoxStyle.Render(strings.Join(lines, "\n"))
}

// plural picks singular or plural based on n, for simple "N entry/entries"
// style messages.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// row formats one fixed-width table row as a string. Padding is applied
// to the status text BEFORE styling -- styling after padding would corrupt
// alignment, since %-Ns counts the invisible ANSI escape bytes too.
func row(status lipgloss.Style, statusText, name, age, detail string) string {
	paddedStatus := fmt.Sprintf("%-8s", statusText)
	return fmt.Sprintf("%s %-28s %-8s %s", status.Render(paddedStatus), name, age, detail)
}

func banner(title string) {
	if out.Len() > 0 {
		fmt.Fprintln(&out)
	}
	fmt.Fprintln(&out, bannerStyle.Render(title))
}

// boxWithTitle draws a rounded-border box with the title embedded directly
// in the top border line (e.g. "╭─ Ban expiration ──────╮"), since
// lipgloss's Border() doesn't support this natively. Content lines are
// left-aligned and padded to a common width; padding matches the 2-space
// left/right padding used elsewhere (summaryBoxStyle), plus one blank line
// above and below the content.
func boxWithTitle(title string, lines ...string) string {
	const pad = 2

	lines = append(append([]string{""}, lines...), "")

	maxContentWidth := 0
	for _, l := range lines {
		maxContentWidth = max(maxContentWidth, lipgloss.Width(l))
	}
	innerWidth := maxContentWidth + pad*2

	leadSeg := "─ "
	trailSeg := " "
	usedLen := lipgloss.Width(leadSeg) + lipgloss.Width(title) + lipgloss.Width(trailSeg)
	dashesRemaining := max(innerWidth-usedLen, 1)

	var b strings.Builder
	b.WriteString(boxBorderStyle.Render("╭"+leadSeg) + titleColorStyle.Render(title) +
		boxBorderStyle.Render(trailSeg+strings.Repeat("─", dashesRemaining)+"╮") + "\n")

	for _, l := range lines {
		padding := strings.Repeat(" ", maxContentWidth-lipgloss.Width(l))
		b.WriteString(boxBorderStyle.Render("│") + strings.Repeat(" ", pad) + l + padding +
			strings.Repeat(" ", pad) + boxBorderStyle.Render("│") + "\n")
	}

	b.WriteString(boxBorderStyle.Render("╰" + strings.Repeat("─", innerWidth) + "╯"))
	return b.String()
}

type addressObject struct {
	Name    string `json:"name"`
	Comment string `json:"comment"`
}

type addressListResponse struct {
	Results []addressObject `json:"results"`
}

type groupMember struct {
	Name string `json:"name"`
}

type addrgrpObject struct {
	Name   string        `json:"name"`
	Member []groupMember `json:"member"`
}

type addrgrpListResponse struct {
	Results []addrgrpObject `json:"results"`
}

type apiClient struct {
	http  *http.Client
	host  string
	port  int
	token string
}

func newAPIClient(host string, port int, token string, insecure bool) *apiClient {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}
	return &apiClient{
		http:  &http.Client{Transport: tr, Timeout: 20 * time.Second},
		host:  host,
		port:  port,
		token: token,
	}
}

func (c *apiClient) do(method, path string, body []byte) (int, []byte, error) {
	url := fmt.Sprintf("https://%s:%d/api/v2/cmdb/%s", c.host, c.port, path)
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

// groupNames lists every address group on the box. Only used to make a
// "no such group" error actionable -- a typo'd -group is otherwise just a
// bare 404, with no hint at what the real name was. Best-effort: if the
// lookup itself fails there is simply nothing extra to show.
func groupNames(c *apiClient) []string {
	status, body, err := c.do("GET", "firewall/addrgrp", nil)
	if err != nil || status != 200 {
		return nil
	}
	var resp addrgrpListResponse
	if json.Unmarshal(body, &resp) != nil {
		return nil
	}
	names := make([]string, 0, len(resp.Results))
	for _, g := range resp.Results {
		names = append(names, g.Name)
	}
	return names
}

func parseEpochNs(comment string) (int64, bool) {
	m := commentPrefixRe.FindStringSubmatch(comment)
	if m == nil {
		return 0, false
	}
	ns, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return ns, true
}

// readToken either returns the explicitly supplied token or reads it from
// standard input. The latter keeps the secret out of the process command line,
// which is useful when the program is launched by Task Scheduler.
func readToken(token string, fromStdin bool, stdin io.Reader) (string, error) {
	if !fromStdin {
		return token, nil
	}
	if token != "" {
		return "", fmt.Errorf("use either -token or -token-stdin, not both")
	}

	input, err := io.ReadAll(io.LimitReader(stdin, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read token from standard input: %w", err)
	}
	if len(input) > maxTokenBytes {
		return "", fmt.Errorf("token from standard input exceeds %d bytes", maxTokenBytes)
	}

	token = strings.TrimSpace(string(input))
	if token == "" {
		return "", fmt.Errorf("token from standard input is empty")
	}
	return token, nil
}

func deleteAddress(c *apiClient, name string) bool {
	status, body, err := c.do("DELETE", "firewall/address/"+name, nil)
	if err != nil || status != 200 {
		fmt.Fprintf(&out, "  %s %s -- %s\n", failStyle.Render("[FAIL]"), name, summarizeError(status, body, err))
		return false
	}
	fmt.Fprintf(&out, "  %s %s\n", okStyle.Render("[DELETED]"), name)
	return true
}

// summarizeError turns a failed API response into a short, actionable
// message. FortiGate returns raw HTML (not JSON) when auth is rejected
// before the request ever reaches the REST handler -- e.g. a bad/expired
// token, or a source IP not permitted to reach the admin/API interface at
// all. Dumping that HTML to the terminal is just noise, so detect it and
// explain what's actually likely wrong instead.
func summarizeError(status int, body []byte, err error) string {
	if err != nil {
		return err.Error()
	}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "<") {
		if status == 401 {
			return "401 Unauthorized (non-JSON response -- request never reached the REST API handler). " +
				"Check: token is valid/not expired, the source IP running this script is permitted " +
				"(trusted-hosts / local-in-policy on the admin interface), and -port matches the admin HTTPS port."
		}
		return fmt.Sprintf("HTTP %d (non-JSON HTML response -- likely blocked before reaching the API, not a normal API error)", status)
	}

	// A FortiGate JSON error is mostly bookkeeping (revision, serial, build,
	// vdom...) that tells the operator nothing. Keep only the parts that
	// identify what was rejected.
	var apiErr struct {
		Status string `json:"status"`
		Path   string `json:"path"`
		Name   string `json:"name"`
		Mkey   string `json:"mkey"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Status == "error" {
		target := strings.Trim(apiErr.Path+"/"+apiErr.Name+"/"+apiErr.Mkey, "/")
		if target != "" {
			return fmt.Sprintf("HTTP %d on %s", status, target)
		}
		return fmt.Sprintf("HTTP %d", status)
	}

	if len(trimmed) > 300 {
		trimmed = trimmed[:300] + "..."
	}
	return fmt.Sprintf("HTTP %d %s", status, trimmed)
}

func main() {
	host := flag.String("host", "", "FortiGate management IP or hostname (required)")
	port := flag.Int("port", 443, "FortiGate admin HTTPS port (default: 443)")
	token := flag.String("token", "", "FortiGate REST API token")
	tokenStdin := flag.Bool("token-stdin", false, "Read FortiGate REST API token from standard input")
	group := flag.String("group", "admin-failed-login", "Address group name to age-expire")
	days := flag.Float64("days", 7, "Age threshold in days before an autoban: entry expires")
	dryRun := flag.Bool("dry-run", false, "Show what would change, make no changes")
	insecure := flag.Bool("insecure", true, "Skip TLS verification (default true, self-signed FortiGate certs)")
	flag.Parse()

	resolvedToken, err := readToken(*token, *tokenStdin, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(1)
	}
	if *host == "" || resolvedToken == "" {
		fmt.Println("Usage: expire_autoban -host <ip> (-token <api_token> | -token-stdin) [-port 443] [-group admin-failed-login] [-days 7] [-dry-run]")
		os.Exit(1)
	}
	if math.IsNaN(*days) || math.IsInf(*days, 0) || *days < 0 {
		fmt.Fprintln(os.Stderr, "ERROR: -days must be a finite number greater than or equal to 0.")
		os.Exit(1)
	}

	client := newAPIClient(*host, *port, resolvedToken, *insecure)
	thresholdSeconds := *days * secondsPerDay
	now := time.Now().Unix()

	status, body, err := client.do("GET", "firewall/addrgrp/"+*group, nil)
	if status == 404 {
		fmt.Fprintf(os.Stderr, "ERROR: address group '%s' does not exist on %s.\n", *group, *host)
		if names := groupNames(client); len(names) > 0 {
			fmt.Fprintf(os.Stderr, "\nAddress groups that do exist:\n")
			for _, n := range names {
				fmt.Fprintf(os.Stderr, "  %s\n", n)
			}
		}
		os.Exit(1)
	}
	if err != nil || status != 200 {
		fmt.Fprintf(os.Stderr, "ERROR: failed to fetch group '%s': %s\n", *group, summarizeError(status, body, err))
		os.Exit(1)
	}

	var grpResp addrgrpListResponse
	if err := json.Unmarshal(body, &grpResp); err != nil || len(grpResp.Results) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: could not parse group response: %v\n", err)
		os.Exit(1)
	}

	members := grpResp.Results[0].Member

	var keepMembers []groupMember
	var toDelete []string
	keptCount, warnCount := 0, 0

	// Fetch and classify every member up front -- nothing here writes to
	// `out` directly, so the full picture (including toDelete's length)
	// is known before anything gets rendered, letting the sections below
	// print in whatever order reads best.
	var tableLines []string
	tableLines = append(tableLines, headerStyle.Render(fmt.Sprintf("%-8s %-28s %-8s %s", "DECISION", "NAME", "AGE", "DETAIL")))

	for _, m := range members {
		status, body, err := client.do("GET", "firewall/address/"+m.Name, nil)
		if err != nil || status != 200 {
			tableLines = append(tableLines, row(warnStyle, "warn", m.Name, "-", fmt.Sprintf("fetch failed (HTTP %d), left in group", status)))
			keepMembers = append(keepMembers, m)
			warnCount++
			continue
		}
		var addrResp addressListResponse
		if err := json.Unmarshal(body, &addrResp); err != nil || len(addrResp.Results) == 0 {
			keepMembers = append(keepMembers, m)
			continue
		}
		comment := addrResp.Results[0].Comment

		ns, ok := parseEpochNs(comment)
		if !ok {
			tableLines = append(tableLines, row(keepStyle, "keep", m.Name, "-", "manual entry, never expires"))
			keepMembers = append(keepMembers, m)
			keptCount++
			continue
		}

		createdSeconds := ns / 1_000_000_000
		ageSeconds := now - createdSeconds
		ageDays := float64(ageSeconds) / secondsPerDay
		bannedStr := time.Unix(createdSeconds, 0).UTC().Format("2006-01-02 15:04")

		if float64(ageSeconds) > thresholdSeconds {
			tableLines = append(tableLines, row(expireStyle, "expire", m.Name, fmt.Sprintf("%.1fd", ageDays), fmt.Sprintf("banned %s, over %.0fd", bannedStr, *days)))
			toDelete = append(toDelete, m.Name)
		} else {
			tableLines = append(tableLines, row(keepStyle, "keep", m.Name, fmt.Sprintf("%.1fd", ageDays), fmt.Sprintf("banned %s, within %.0fd", bannedStr, *days)))
			keepMembers = append(keepMembers, m)
			keptCount++
		}
	}

	// --- render, in display order: dry-run note -> PARAMETERS -> STATUS -> apply -> SUMMARY ---

	if *dryRun {
		if len(toDelete) == 0 {
			dryRunNote("Nothing to expire this time.")
		} else {
			dryRunNote("%d %s would be expired.", len(toDelete), plural(len(toDelete), "entry", "entries"))
		}
	}

	banner("PARAMETERS")
	fmt.Fprintln(&out, kvBox(
		"group:", *group,
		"age:", fmt.Sprintf("%.0fd", *days),
	))

	banner("STATUS")
	if len(members) == 0 {
		fmt.Fprintln(&out, dimStyle.Render(fmt.Sprintf("'%s' has no members.", *group)))
	} else {
		fmt.Fprintln(&out, tableBoxStyle.Render(strings.Join(tableLines, "\n")))
	}

	deleteFailures := 0
	if !*dryRun && len(toDelete) > 0 {
		newMemberJSON, _ := json.Marshal(map[string]any{"member": keepMembers})
		status, body, err := client.do("PUT", "firewall/addrgrp/"+*group, newMemberJSON)
		if err != nil || status != 200 {
			fmt.Fprintf(os.Stderr, "ERROR: failed to update group membership: %s\n", summarizeError(status, body, err))
			os.Exit(1)
		}
		fmt.Fprintf(&out, "\n%s Group '%s' updated, %d %s removed:\n", okStyle.Render("[OK]"), *group, len(toDelete), plural(len(toDelete), "member", "members"))

		for _, name := range toDelete {
			if !deleteAddress(client, name) {
				deleteFailures++
			}
		}
	}

	banner("SUMMARY")
	mode := "APPLIED"
	if *dryRun {
		mode = "DRY RUN"
	}
	summary := []string{
		"mode:", mode,
		"kept:", strconv.Itoa(keptCount),
		"expired:", strconv.Itoa(len(toDelete)),
	}
	if warnCount > 0 {
		summary = append(summary, "warnings:", fmt.Sprintf("%d (see warn rows above)", warnCount))
	}
	if deleteFailures > 0 {
		summary = append(summary, "delete failures:", strconv.Itoa(deleteFailures))
	}
	fmt.Fprintln(&out, kvBox(summary...))

	fmt.Println(boxWithTitle("Ban expiration", strings.Split(strings.TrimRight(out.String(), "\n"), "\n")...))
	if deleteFailures > 0 {
		os.Exit(1)
	}
}
