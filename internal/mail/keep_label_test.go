package mail

import "testing"

// TestBuildLabelsAppliesKeepOnlyWhenAsked is the two-verdict form: the same
// helper must be able to produce a label set WITH and WITHOUT the marker. A test
// that only checks the present case cannot distinguish a working flag from one
// that labels everything.
func TestBuildLabelsAppliesKeepOnlyWhenAsked(t *testing.T) {
	r := &Router{}

	kept := r.buildLabels(&Message{From: "mayor/", Type: TypeNotification, Keep: true})
	if !containsLabel(kept, "gt:keep") {
		t.Errorf("Keep=true produced %v, missing gt:keep", kept)
	}

	plain := r.buildLabels(&Message{From: "mayor/", Type: TypeNotification})
	if containsLabel(plain, "gt:keep") {
		t.Errorf("Keep=false produced %v, must not carry gt:keep", plain)
	}
	if !containsLabel(plain, "gt:message") {
		t.Errorf("Keep=false lost the gt:message label: %v", plain)
	}
}

// TestKeepLabelsCoversEveryFanOutRoute. Direct mail is not the only way a
// verdict travels — a ruling announced to a channel or dropped on a queue is the
// same record. buildLabels covers sendToSingle; the other three routes build
// their own slices and call keepLabels directly, so this pins the helper they
// all share.
func TestKeepLabelsCoversEveryFanOutRoute(t *testing.T) {
	if got := keepLabels(&Message{Keep: true}); len(got) != 1 || got[0] != "gt:keep" {
		t.Errorf("keepLabels(Keep=true) = %v, want [gt:keep]", got)
	}
	if got := keepLabels(&Message{}); len(got) != 0 {
		t.Errorf("keepLabels(Keep=false) = %v, want empty", got)
	}
	if got := keepLabels(nil); len(got) != 0 {
		t.Errorf("keepLabels(nil) = %v, want empty", got)
	}
}

// TestKeepIsNotTheWispAxis pins the distinction the whole of dbt-kk9 turns on.
// Wisp picks the storage tier; Keep picks the retention policy. If a future
// change collapses them, --permanent silently starts meaning "never deleted"
// (which unbounds the issues table) or --keep silently stops protecting.
func TestKeepIsNotTheWispAxis(t *testing.T) {
	r := &Router{}

	// A non-wisp (i.e. --permanent) message must NOT be protected by that alone.
	permanent := &Message{From: "mayor/", Type: TypeNotification, Wisp: false}
	if containsLabel(r.buildLabels(permanent), "gt:keep") {
		t.Error("a non-wisp message acquired gt:keep implicitly — --permanent must not imply retention")
	}
}
