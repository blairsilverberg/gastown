// This test lives in the external test package on purpose: frames inside
// internal/telemetry are filtered out of caller.stack, so the call site can
// only be observed from outside the package — which is also how the real
// emitters (internal/tmux, internal/polecat, internal/cmd) see it.
package telemetry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/telemetry"
)

func TestRecordPromptSend_StackNamesTheExternalCallSite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(telemetry.EnvSinkDir, dir)

	telemetry.RecordPromptSend(t.Context(), "sess-ext", "payload", 100, nil)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading sink dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 sink file, got %d", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading sink file: %v", err)
	}
	var rec struct {
		Session string `json:"session"`
		Caller  struct {
			Stack []string `json:"stack"`
			Cmd   []string `json:"cmd"`
		} `json:"caller"`
	}
	line := strings.TrimSpace(string(raw))
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("sink line is not valid JSON (%q): %v", line, err)
	}
	if rec.Session != "sess-ext" {
		t.Errorf("session = %q, want sess-ext", rec.Session)
	}
	if len(rec.Caller.Stack) == 0 {
		t.Fatal("caller.stack is empty; it is the field that identifies what ran the send")
	}
	if !strings.Contains(rec.Caller.Stack[0], "TestRecordPromptSend_StackNamesTheExternalCallSite") {
		t.Errorf("caller.stack[0] = %q, want the calling test function", rec.Caller.Stack[0])
	}
	if len(rec.Caller.Cmd) == 0 {
		t.Error("caller.cmd is empty")
	}
}
