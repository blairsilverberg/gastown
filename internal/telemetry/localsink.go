// Package telemetry — localsink.go
//
// An always-on, collector-free local sink for the telemetry gt already emits.
//
// Why this exists: every Record* helper in recorder.go emits through the OTel
// global logger/meter providers, which stay no-op unless telemetry.Init found
// GT_OTEL_METRICS_URL or GT_OTEL_LOGS_URL. On a box with no collector — the
// steady state for most Gas Town installs — those records are generated and
// thrown away. Forensics then have to reason from pane renders instead of
// reading a log (see openclaw op-oju6 / capital cap-o1zz).
//
// The sink is append-only JSONL under <townRoot>/.runtime/telemetry/, needs no
// collector, and is independent of the OTel path: it fires whether or not OTLP
// is configured, and OTLP export is unaffected by it.
//
// Privacy: the sink NEVER records prompt/keystroke text. Those buffers carry
// approval-shaped strings and credentials-adjacent operator input. Only the
// byte length is kept. Process command lines are recorded with every argument
// value redacted to "<redacted:N>" — the subcommand chain and flag *names*
// survive (that is what identifies the caller), the values do not.
//
// Retention is bounded: one file per UTC day, rotated at a byte cap, and files
// older than the retention window are pruned.
package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// EnvSinkDir overrides the sink directory outright. When set, no town-root
	// discovery happens and records are written here.
	EnvSinkDir = "GT_TELEMETRY_SINK_DIR"

	// EnvSinkDisable disables the local sink when set to "off", "0", or "false".
	// The sink is ON by default — that is the whole point of it.
	EnvSinkDisable = "GT_TELEMETRY_SINK"

	// EnvSinkRetentionDays bounds how many days of sink files are kept.
	EnvSinkRetentionDays = "GT_TELEMETRY_SINK_RETENTION_DAYS"

	// EnvSinkMaxBytes bounds the size of a single day's sink file before it is
	// rotated to "<name>.1" (at most two files per day survive).
	EnvSinkMaxBytes = "GT_TELEMETRY_SINK_MAX_BYTES"

	// sinkSubdir is the directory under <townRoot>/.runtime that holds sink files.
	sinkSubdir = "telemetry"

	// defaultSinkRetentionDays is the default retention window in days.
	defaultSinkRetentionDays = 7

	// defaultSinkMaxBytes is the default per-day file cap (32 MiB).
	defaultSinkMaxBytes int64 = 32 << 20

	// sinkPruneEvery re-runs pruning after this many writes in a process.
	sinkPruneEvery = 512

	// maxStackFrames bounds how many gastown call frames are recorded.
	maxStackFrames = 8

	// maxRedactedArgs bounds how many argv elements are recorded.
	maxRedactedArgs = 24

	// maxSubcommandChain bounds how many leading bare tokens are kept verbatim.
	// Two covers gt's deepest common form ("gt session inject", "gt mail send")
	// while keeping short positional payloads out: in `gt nudge <target> ok`,
	// "ok" is the third bare token and is therefore redacted. Deeper
	// subcommands lose their tail to redaction — an acceptable trade, since
	// caller.stack names the call site precisely anyway.
	maxSubcommandChain = 2
)

// sinkRecord is one JSONL line. Field names are snake_case to match the OTel
// attribute names in docs/otel-data-model.md.
type sinkRecord struct {
	TS         string      `json:"ts"`    // RFC3339Nano, UTC
	Event      string      `json:"event"` // e.g. "prompt.send"
	Session    string      `json:"session,omitempty"`
	KeysLen    int         `json:"keys_len"`
	DebounceMs int         `json:"debounce_ms"`
	Status     string      `json:"status"`
	Error      string      `json:"error,omitempty"`
	RunID      string      `json:"run_id,omitempty"`
	GTSession  string      `json:"gt_session,omitempty"` // GT_SESSION of the emitting process
	GTRole     string      `json:"gt_role,omitempty"`    // GT_ROLE of the emitting process
	Caller     *callerInfo `json:"caller,omitempty"`
}

// callerInfo identifies what ran the send. This is the field that answers
// "which process wrote text into which pane", so it is the reason the sink
// exists at all.
type callerInfo struct {
	PID    int      `json:"pid"`
	PPID   int      `json:"ppid"`
	Exe    string   `json:"exe,omitempty"`        // basename of argv[0]
	Cmd    []string `json:"cmd,omitempty"`        // own cmdline, values redacted
	Parent []string `json:"parent_cmd,omitempty"` // parent cmdline, values redacted
	Stack  []string `json:"stack,omitempty"`      // gastown call frames, innermost first
}

// sink state: resolved once per process.
var (
	sinkDirOnce  sync.Once
	sinkDirPath  string
	sinkWriteMu  sync.Mutex
	sinkWrites   atomic.Uint64
	sinkPruneMu  sync.Mutex
	sinkPrunedAt atomic.Bool
)

// sinkDisabled reports whether the operator turned the sink off, or whether we
// are inside `go test` (tests must not append to a real town's sink).
func sinkDisabled() bool {
	if testing.Testing() {
		// Tests opt in explicitly by setting GT_TELEMETRY_SINK_DIR.
		return os.Getenv(EnvSinkDir) == ""
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvSinkDisable))) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

// findTownRootLocal walks up from startDir looking for mayor/town.json and
// returns the outermost match, mirroring beads.FindTownRoot. Duplicated here
// because internal/beads imports this package — importing it back would cycle.
func findTownRootLocal(startDir string) string {
	dir := startDir
	candidate := ""
	for {
		if _, err := os.Stat(filepath.Join(dir, "mayor", "town.json")); err == nil {
			candidate = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return candidate
		}
		dir = parent
	}
}

// resolveSinkDir determines the sink directory, or "" when there is nowhere
// sensible to write (no town root found → sink stays silent rather than
// littering unrelated filesystems).
func resolveSinkDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvSinkDir)); dir != "" {
		return dir
	}
	townRoot := ""
	if cwd, err := os.Getwd(); err == nil {
		townRoot = findTownRootLocal(cwd)
	}
	if townRoot == "" {
		// Daemons and hooks may run with a cwd outside the town; fall back to
		// the env the shell integration and session spawner both export.
		for _, key := range []string{"GT_TOWN_ROOT", "GT_ROOT", "GT_TOWN"} {
			if v := strings.TrimSpace(os.Getenv(key)); v != "" {
				if _, err := os.Stat(filepath.Join(v, "mayor", "town.json")); err == nil {
					townRoot = v
					break
				}
			}
		}
	}
	if townRoot == "" {
		return ""
	}
	return filepath.Join(townRoot, ".runtime", sinkSubdir)
}

// sinkDir returns the resolved sink directory, or "" when the sink is off.
// Only the *discovered* path is cached; an explicit GT_TELEMETRY_SINK_DIR is
// re-read every call, since honouring it costs no filesystem walk.
func sinkDir() string {
	if sinkDisabled() {
		return ""
	}
	if dir := strings.TrimSpace(os.Getenv(EnvSinkDir)); dir != "" {
		return dir
	}
	sinkDirOnce.Do(func() { sinkDirPath = resolveSinkDir() })
	return sinkDirPath
}

// redactCmdline renders a command line with every argument *value* replaced by
// "<redacted:N>" (N = value length in bytes). The executable basename, the
// leading subcommand chain, and flag names are preserved — together they say
// what ran, without reproducing operator text that may have been the payload.
func redactCmdline(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(argv), maxRedactedArgs))
	out = append(out, filepath.Base(argv[0]))

	i := 1
	// Leading subcommand chain: bare, short, shell-safe tokens before any flag,
	// capped at maxSubcommandChain so a short positional message can't sneak
	// through as a "subcommand".
	for ; i < len(argv) && i <= maxSubcommandChain; i++ {
		if !isSubcommandToken(argv[i]) {
			break
		}
		out = append(out, argv[i])
	}
	// Everything after the first flag: flag names survive, values do not.
	for ; i < len(argv) && len(out) < maxRedactedArgs; i++ {
		arg := argv[i]
		switch {
		case strings.HasPrefix(arg, "-"):
			name, val, hasVal := strings.Cut(arg, "=")
			if hasVal {
				out = append(out, name+"="+redactedLen(len(val)))
			} else {
				out = append(out, name)
			}
		default:
			out = append(out, redactedLen(len(arg)))
		}
	}
	if i < len(argv) {
		out = append(out, redactedLen(-1))
	}
	return out
}

// isSubcommandToken reports whether arg looks like a cobra subcommand rather
// than a value: bare, short, and drawn from a conservative character set.
func isSubcommandToken(arg string) bool {
	if arg == "" || len(arg) > 24 || strings.HasPrefix(arg, "-") {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// redactedLen renders a redaction placeholder; n < 0 means "length unknown".
func redactedLen(n int) string {
	if n < 0 {
		return "<redacted>"
	}
	return "<redacted:" + itoa(n) + ">"
}

// itoa is strconv.Itoa without the import churn in a hot-ish path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// parentCmdline reads the parent process command line on Linux, redacted.
// Returns nil on other platforms or when /proc is unavailable.
func parentCmdline(ppid int) []string {
	if ppid <= 0 {
		return nil
	}
	raw, err := os.ReadFile("/proc/" + itoa(ppid) + "/cmdline")
	if err != nil || len(raw) == 0 {
		return nil
	}
	parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	return redactCmdline(parts)
}

// callStack returns the gastown call frames above this package, innermost
// first, formatted as "pkg.Func (file.go:line)". Frames inside the telemetry
// package are dropped so the first entry is the real emitter.
func callStack(skip int) []string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip, pcs)
	if n == 0 {
		return nil
	}
	frames := runtime.CallersFrames(pcs[:n])
	const modPrefix = "github.com/steveyegge/gastown/"
	var out []string
	for len(out) < maxStackFrames {
		frame, more := frames.Next()
		if frame.Function == "" && !more {
			break
		}
		fn := strings.TrimPrefix(frame.Function, modPrefix)
		// Keep gastown frames only, and drop this package's own frames so the
		// first entry is the real emitter rather than the recorder plumbing.
		if strings.HasPrefix(frame.Function, modPrefix) && !strings.HasPrefix(fn, "internal/telemetry.") {
			out = append(out, fn+" ("+filepath.Base(frame.File)+":"+itoa(frame.Line)+")")
		}
		if !more {
			break
		}
	}
	return out
}

// currentCaller snapshots the emitting process and its call stack.
func currentCaller(skip int) *callerInfo {
	ppid := os.Getppid()
	c := &callerInfo{
		PID:    os.Getpid(),
		PPID:   ppid,
		Cmd:    redactCmdline(os.Args),
		Parent: parentCmdline(ppid),
		Stack:  callStack(skip),
	}
	if len(os.Args) > 0 {
		c.Exe = filepath.Base(os.Args[0])
	}
	return c
}

// sinkRetentionDays returns the configured retention window, minimum 1 day.
func sinkRetentionDays() int {
	d := envInt(EnvSinkRetentionDays, defaultSinkRetentionDays)
	if d < 1 {
		d = 1
	}
	return d
}

// sinkMaxBytes returns the configured per-day file cap, minimum 4 KiB.
func sinkMaxBytes() int64 {
	b := int64(envInt(EnvSinkMaxBytes, int(defaultSinkMaxBytes)))
	if b < 4096 {
		b = 4096
	}
	return b
}

// sinkFileName is the file for the given event on the given UTC day.
func sinkFileName(event string, now time.Time) string {
	safe := strings.NewReplacer(".", "-", "/", "-", string(filepath.Separator), "-").Replace(event)
	return safe + "-" + now.UTC().Format("2006-01-02") + ".jsonl"
}

// writeSink appends one JSON line for rec. Best-effort: all errors are
// swallowed, since telemetry must never affect gt's behaviour.
func writeSink(event string, rec sinkRecord) {
	dir := sinkDir()
	if dir == "" {
		return
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	line = append(line, '\n')

	sinkWriteMu.Lock()
	defer sinkWriteMu.Unlock()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, sinkFileName(event, time.Now()))

	// Rotate before appending when the day's file is over the cap. At most two
	// files per day survive (<name>.jsonl and <name>.jsonl.1).
	if fi, statErr := os.Stat(path); statErr == nil && fi.Size() >= sinkMaxBytes() {
		_ = os.Rename(path, path+".1")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	// Single write of a complete line: concurrent O_APPEND writers do not
	// interleave partial records.
	_, _ = f.Write(line)
	_ = f.Close()

	// Prune synchronously. Most writers are short-lived `gt` CLI processes that
	// exit before a background goroutine would ever be scheduled, so an async
	// prune means retention is never enforced in practice (caught at runtime:
	// a 30-day-old file survived every CLI write).
	n := sinkWrites.Add(1)
	if sinkPrunedAt.CompareAndSwap(false, true) || n%sinkPruneEvery == 0 {
		pruneSink(dir)
	}
}

// pruneSink deletes sink files whose mtime is older than the retention window.
// Only files this package writes (*.jsonl and rotated *.jsonl.1) are touched.
func pruneSink(dir string) {
	sinkPruneMu.Lock()
	defer sinkPruneMu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(sinkRetentionDays()) * 24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".jsonl.1") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// recordPaneWriteLocal writes one pane-write record to the local JSONL sink.
// Called unconditionally from the Record* helpers so the record survives on
// boxes with no OTLP collector configured. keys is passed for its LENGTH ONLY
// — the text is never written.
//
// Frames inside this package are filtered out of the recorded stack, so the
// skip depth only needs to be small enough not to lose the real emitter.
func recordPaneWriteLocal(event, runID, session, keys string, debounceMs int, err error) {
	if sinkDir() == "" {
		return
	}
	rec := sinkRecord{
		TS:         time.Now().UTC().Format(time.RFC3339Nano),
		Event:      event,
		Session:    session,
		KeysLen:    len(keys),
		DebounceMs: debounceMs,
		Status:     statusStr(err),
		RunID:      runID,
		GTSession:  os.Getenv("GT_SESSION"),
		GTRole:     os.Getenv("GT_ROLE"),
		Caller:     currentCaller(3),
	}
	if err != nil {
		rec.Error = err.Error()
	}
	writeSink(rec.Event, rec)
}
