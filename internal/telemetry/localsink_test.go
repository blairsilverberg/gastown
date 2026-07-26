package telemetry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// useSinkDir points the local sink at a temp dir for the duration of the test
// and resets the process-wide once/counters so each test resolves it freshly.
func useSinkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvSinkDir, dir)
	resetSinkState(t)
	return dir
}

func resetSinkState(t *testing.T) {
	t.Helper()
	sinkDirOnce = sync.Once{}
	sinkDirPath = ""
	sinkPrunedAt.Store(false)
	sinkWrites.Store(0)
	t.Cleanup(func() {
		sinkDirOnce = sync.Once{}
		sinkDirPath = ""
		sinkPrunedAt.Store(false)
		sinkWrites.Store(0)
	})
}

// readSinkLines returns every JSONL line written under dir, across all files.
func readSinkLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading sink dir: %v", err)
	}
	var out []map[string]any
	for _, e := range entries {
		if e.IsDir() || !strings.Contains(e.Name(), ".jsonl") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("sink line is not valid JSON (%q): %v", line, err)
			}
			out = append(out, m)
		}
	}
	return out
}

// --- the core guarantee: the record survives with no OTLP configured ---

func TestRecordPromptSend_WritesLocalSinkWithNoOTLPConfigured(t *testing.T) {
	resetInstruments(t)
	// Explicitly unset both OTLP endpoints: this is the box's steady state and
	// the exact condition under which the record used to be discarded.
	t.Setenv(EnvMetricsURL, "")
	t.Setenv(EnvLogsURL, "")
	dir := useSinkDir(t)

	RecordPromptSend(WithRunID(t.Context(), "run-xyz"), "gt-openclaw-nux", "approve the thing", 250, nil)

	lines := readSinkLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 sink line, got %d", len(lines))
	}
	rec := lines[0]
	if rec["event"] != "prompt.send" {
		t.Errorf("event = %v, want prompt.send", rec["event"])
	}
	if rec["session"] != "gt-openclaw-nux" {
		t.Errorf("session = %v, want gt-openclaw-nux", rec["session"])
	}
	if rec["keys_len"] != float64(len("approve the thing")) {
		t.Errorf("keys_len = %v, want %d", rec["keys_len"], len("approve the thing"))
	}
	if rec["debounce_ms"] != float64(250) {
		t.Errorf("debounce_ms = %v, want 250", rec["debounce_ms"])
	}
	if rec["status"] != "ok" {
		t.Errorf("status = %v, want ok", rec["status"])
	}
	if rec["run_id"] != "run-xyz" {
		t.Errorf("run_id = %v, want run-xyz", rec["run_id"])
	}
	if _, err := time.Parse(time.RFC3339Nano, rec["ts"].(string)); err != nil {
		t.Errorf("ts %q is not RFC3339Nano: %v", rec["ts"], err)
	}
}

// The prompt text is the one thing that must never land on disk: these buffers
// carry approval-shaped strings and credentials-adjacent operator input.
func TestRecordPromptSend_NeverWritesKeysText(t *testing.T) {
	resetInstruments(t)
	dir := useSinkDir(t)
	const secret = "SUPERSECRET-approve-wire-transfer"

	// GT_LOG_PROMPT_KEYS only ever governed the OTel path; the sink must ignore it.
	t.Setenv("GT_LOG_PROMPT_KEYS", "true")
	RecordPromptSend(t.Context(), "sess", secret, 100, nil)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading sink dir: %v", err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("sink file %s contains the prompt text", e.Name())
		}
	}
	lines := readSinkLines(t, dir)
	if len(lines) != 1 || lines[0]["keys_len"] != float64(len(secret)) {
		t.Fatalf("expected 1 line with keys_len=%d, got %v", len(secret), lines)
	}
	if _, ok := lines[0]["keys"]; ok {
		t.Error("sink record has a \"keys\" field; it must never carry prompt text")
	}
}

// The caller is the field that answers "what ran inject".
func TestRecordPromptSend_RecordsCaller(t *testing.T) {
	resetInstruments(t)
	dir := useSinkDir(t)

	RecordPromptSend(t.Context(), "sess", "x", 0, nil)

	lines := readSinkLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("expected 1 sink line, got %d", len(lines))
	}
	caller, ok := lines[0]["caller"].(map[string]any)
	if !ok {
		t.Fatal("sink record has no caller object")
	}
	if pid, _ := caller["pid"].(float64); int(pid) != os.Getpid() {
		t.Errorf("caller.pid = %v, want %d", caller["pid"], os.Getpid())
	}
	if ppid, _ := caller["ppid"].(float64); int(ppid) != os.Getppid() {
		t.Errorf("caller.ppid = %v, want %d", caller["ppid"], os.Getppid())
	}
	if exe, _ := caller["exe"].(string); exe == "" {
		t.Error("caller.exe is empty")
	}
	if cmd, _ := caller["cmd"].([]any); len(cmd) == 0 {
		t.Error("caller.cmd is empty")
	}
	// Frames inside this package are filtered, so an in-package caller yields
	// no stack. TestRecordPromptSend_StackNamesTheExternalCallSite (external
	// test package) covers the populated case.
	stack, _ := caller["stack"].([]any)
	for _, frame := range stack {
		if s, _ := frame.(string); strings.Contains(s, "internal/telemetry.") {
			t.Errorf("stack frame %q is inside the telemetry package; should be filtered", s)
		}
	}
}

func TestRecordPromptSend_ErrorStatus(t *testing.T) {
	resetInstruments(t)
	dir := useSinkDir(t)

	RecordPromptSend(t.Context(), "sess", "x", 0, errors.New("no such session"))

	lines := readSinkLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("expected 1 sink line, got %d", len(lines))
	}
	if lines[0]["status"] != "error" {
		t.Errorf("status = %v, want error", lines[0]["status"])
	}
	if lines[0]["error"] != "no such session" {
		t.Errorf("error = %v, want %q", lines[0]["error"], "no such session")
	}
}

// --- cmdline redaction ---

func TestRedactCmdline_RedactsValuesKeepsShape(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{
			// The target is redacted too — no loss, the session field carries it.
			name: "inject payload is redacted, subcommand chain survives",
			argv: []string{"/usr/local/bin/gt", "session", "inject", "nux", "-m", "yes, ship it"},
			want: []string{"gt", "session", "inject", "<redacted:3>", "-m", "<redacted:12>"},
		},
		{
			// The failure mode this bounds: a short bare message must not be
			// mistaken for a subcommand. "ok" is the third bare token.
			name: "short positional message is redacted",
			argv: []string{"gt", "nudge", "furiosa", "ok"},
			want: []string{"gt", "nudge", "furiosa", "<redacted:2>"},
		},
		{
			name: "subcommand chain is capped at two levels",
			argv: []string{"gt", "polecat", "session", "start"},
			want: []string{"gt", "polecat", "session", "<redacted:5>"},
		},
		{
			name: "flag=value keeps the flag name only",
			argv: []string{"gt", "nudge", "--message=approve"},
			want: []string{"gt", "nudge", "--message=<redacted:7>"},
		},
		{
			name: "bare long payload is redacted",
			argv: []string{"gt", "sendkeys", strings.Repeat("a", 40)},
			want: []string{"gt", "sendkeys", "<redacted:40>"},
		},
		{
			name: "no args",
			argv: []string{"gt"},
			want: []string{"gt"},
		},
		{
			name: "empty argv",
			argv: nil,
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactCmdline(tc.argv)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("redactCmdline(%q) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

func TestRedactCmdline_BoundsArgCount(t *testing.T) {
	argv := append([]string{"gt", "session", "inject"}, strings.Split(strings.Repeat("x ", 100), " ")...)
	got := redactCmdline(argv)
	if len(got) > maxRedactedArgs+1 {
		t.Errorf("redactCmdline returned %d elements, want ≤ %d", len(got), maxRedactedArgs+1)
	}
}

// --- retention ---

func TestPruneSink_RemovesFilesPastRetention(t *testing.T) {
	dir := useSinkDir(t)
	t.Setenv(EnvSinkRetentionDays, "2")

	old := filepath.Join(dir, "prompt-send-2020-01-01.jsonl")
	rotated := filepath.Join(dir, "prompt-send-2020-01-01.jsonl.1")
	fresh := filepath.Join(dir, "prompt-send-fresh.jsonl")
	unrelated := filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, rotated, fresh, unrelated} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
	stale := time.Now().Add(-72 * time.Hour)
	for _, p := range []string{old, rotated, unrelated} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}

	pruneSink(dir)

	for _, p := range []string{old, rotated} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned", filepath.Base(p))
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh file was pruned: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("pruning removed a non-sink file (%s): %v", filepath.Base(unrelated), err)
	}
}

// Regression: pruning must happen on the writing goroutine. Most writers are
// short-lived `gt` CLI processes that exit before a background goroutine would
// be scheduled, so an async prune leaves retention unenforced — observed at
// runtime with a 30-day-old file surviving every CLI write.
func TestWriteSink_PrunesSynchronously(t *testing.T) {
	dir := useSinkDir(t)
	t.Setenv(EnvSinkRetentionDays, "1")

	stalePath := filepath.Join(dir, "prompt-send-2020-01-01.jsonl")
	if err := os.WriteFile(stalePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("writing stale file: %v", err)
	}
	staleTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stalePath, staleTime, staleTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// No sleep, no goroutine wait: the write itself must have pruned.
	writeSink("prompt.send", sinkRecord{Event: "prompt.send", Status: "ok"})

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Error("stale file survived writeSink; retention is not enforced on the writing goroutine")
	}
}

func TestWriteSink_RotatesAtMaxBytes(t *testing.T) {
	dir := useSinkDir(t)
	t.Setenv(EnvSinkMaxBytes, "4096") // clamped minimum

	for i := 0; i < 40; i++ {
		writeSink("prompt.send", sinkRecord{
			TS:      time.Now().UTC().Format(time.RFC3339Nano),
			Event:   "prompt.send",
			Session: strings.Repeat("s", 200),
			Status:  "ok",
		})
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading sink dir: %v", err)
	}
	var rotated bool
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		if strings.HasSuffix(e.Name(), ".jsonl.1") {
			rotated = true
		}
		if info.Size() > 4096+1024 { // one record of slack past the cap
			t.Errorf("%s is %d bytes, past the rotation cap", e.Name(), info.Size())
		}
	}
	if !rotated {
		t.Error("no rotated .jsonl.1 file; the byte cap did not trigger")
	}
	if len(entries) > 2 {
		t.Errorf("expected at most 2 files for one day, got %d", len(entries))
	}
}

// --- enable/disable + directory resolution ---

func TestSinkDir_EmptyWithoutTownRootOrOverride(t *testing.T) {
	t.Setenv(EnvSinkDir, "")
	resetSinkState(t)
	if got := sinkDir(); got != "" {
		t.Errorf("sinkDir() = %q, want empty when no override is set under test", got)
	}
}

func TestWriteSink_NoOpWhenSinkDirUnset(t *testing.T) {
	t.Setenv(EnvSinkDir, "")
	resetSinkState(t)
	// Must not panic and must not create anything.
	writeSink("prompt.send", sinkRecord{Event: "prompt.send", Status: "ok"})
}

func TestResolveSinkDir_UsesDiscoveredTownRoot(t *testing.T) {
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Join(town, "mayor"), 0o755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(town, "mayor", "town.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing town.json: %v", err)
	}
	rig := filepath.Join(town, "somerig", "polecats", "nux")
	if err := os.MkdirAll(rig, 0o755); err != nil {
		t.Fatalf("mkdir rig: %v", err)
	}
	t.Setenv(EnvSinkDir, "")
	t.Chdir(rig)

	want := filepath.Join(town, ".runtime", sinkSubdir)
	if got := resolveSinkDir(); got != want {
		t.Errorf("resolveSinkDir() = %q, want %q", got, want)
	}
}

func TestResolveSinkDir_FallsBackToTownRootEnv(t *testing.T) {
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Join(town, "mayor"), 0o755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(town, "mayor", "town.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing town.json: %v", err)
	}
	t.Setenv(EnvSinkDir, "")
	t.Setenv("GT_TOWN_ROOT", town)
	t.Chdir(t.TempDir()) // cwd outside any town, as a daemon may be

	want := filepath.Join(town, ".runtime", sinkSubdir)
	if got := resolveSinkDir(); got != want {
		t.Errorf("resolveSinkDir() = %q, want %q", got, want)
	}
}

func TestResolveSinkDir_IgnoresTownRootEnvWithoutTownJSON(t *testing.T) {
	t.Setenv(EnvSinkDir, "")
	t.Setenv("GT_TOWN_ROOT", t.TempDir()) // exists but is not a town
	t.Setenv("GT_ROOT", "")
	t.Setenv("GT_TOWN", "")
	t.Chdir(t.TempDir())

	if got := resolveSinkDir(); got != "" {
		t.Errorf("resolveSinkDir() = %q, want empty for a non-town GT_TOWN_ROOT", got)
	}
}

func TestSinkFileName_PerUTCDay(t *testing.T) {
	ts := time.Date(2026, 7, 26, 23, 59, 0, 0, time.UTC)
	if got := sinkFileName("prompt.send", ts); got != "prompt-send-2026-07-26.jsonl" {
		t.Errorf("sinkFileName = %q, want prompt-send-2026-07-26.jsonl", got)
	}
	// Event names must not be able to escape the sink directory.
	if got := sinkFileName("../../etc/passwd", ts); strings.Contains(got, "/") || strings.Contains(got, string(filepath.Separator)) {
		t.Errorf("sinkFileName = %q, must not contain a path separator", got)
	}
}

func TestWriteSink_ConcurrentWritersProduceWholeLines(t *testing.T) {
	dir := useSinkDir(t)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			writeSink("prompt.send", sinkRecord{
				TS:      time.Now().UTC().Format(time.RFC3339Nano),
				Event:   "prompt.send",
				Session: "sess",
				Status:  "ok",
			})
		}()
	}
	wg.Wait()

	lines := readSinkLines(t, dir) // fails the test if any line is not valid JSON
	if len(lines) != 32 {
		t.Errorf("got %d sink lines, want 32", len(lines))
	}
}
