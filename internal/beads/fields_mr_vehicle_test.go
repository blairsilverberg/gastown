package beads

import (
	"strings"
	"testing"
)

// TestMRFields_DeliveryVehicleRoundTrip pins the provenance an MR wisp must be
// able to carry (op-krtw): which merge strategy it was submitted under, and
// which PR backs it. Without these on the bead, the refinery cannot tell a
// legitimate pr-rig submission from the PR-less wisp that reached the capital
// queue on 2026-07-25.
func TestMRFields_DeliveryVehicleRoundTrip(t *testing.T) {
	original := &MRFields{
		Branch:        "polecat/mica_op-abc",
		Target:        "master",
		SourceIssue:   "op-abc",
		Rig:           "capital",
		MergeStrategy: "pr",
		PRNumber:      21606,
		PRURL:         "https://github.com/acme/capital/pull/21606",
	}

	formatted := FormatMRFields(original)
	for _, want := range []string{"merge_strategy: pr", "pr: 21606", "pr_url: https://github.com/acme/capital/pull/21606"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatted MR fields missing %q:\n%s", want, formatted)
		}
	}

	parsed := ParseMRFields(&Issue{Description: formatted})
	if parsed == nil {
		t.Fatal("ParseMRFields returned nil")
	}
	if parsed.MergeStrategy != "pr" {
		t.Errorf("MergeStrategy = %q, want pr", parsed.MergeStrategy)
	}
	if parsed.PRNumber != 21606 {
		t.Errorf("PRNumber = %d, want 21606", parsed.PRNumber)
	}
	if parsed.PRURL != original.PRURL {
		t.Errorf("PRURL = %q, want %q", parsed.PRURL, original.PRURL)
	}
}

func TestMRFields_PRAliases(t *testing.T) {
	parsed := ParseMRFields(&Issue{Description: "branch: b\npr_number: 42\npr-url: https://example.test/pull/42\nmerge-strategy: PR"})
	if parsed == nil {
		t.Fatal("ParseMRFields returned nil")
	}
	if parsed.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", parsed.PRNumber)
	}
	if parsed.PRURL != "https://example.test/pull/42" {
		t.Errorf("PRURL = %q", parsed.PRURL)
	}
	if parsed.MergeStrategy != "PR" {
		t.Errorf("MergeStrategy = %q, want PR (value preserved verbatim)", parsed.MergeStrategy)
	}
}

// TestMRFields_NoPRReference is the incident shape at the field layer: the MR
// wisp created before this fix carried no PR key at all.
func TestMRFields_NoPRReference(t *testing.T) {
	parsed := ParseMRFields(&Issue{Description: "branch: polecat/mica_op-abc\ntarget: master\nsource_issue: op-abc\nrig: capital"})
	if parsed == nil {
		t.Fatal("ParseMRFields returned nil")
	}
	if parsed.PRNumber != 0 || parsed.PRURL != "" {
		t.Errorf("expected no PR reference, got number=%d url=%q", parsed.PRNumber, parsed.PRURL)
	}
	if parsed.MergeStrategy != "" {
		t.Errorf("expected no declared strategy, got %q", parsed.MergeStrategy)
	}
}

func TestSetMRFields_ReplacesDeliveryVehicleLines(t *testing.T) {
	issue := &Issue{Description: "branch: b\nmerge_strategy: pr\npr: 1\npr_url: https://example.test/pull/1\n\nfree text"}
	updated := SetMRFields(issue, &MRFields{Branch: "b", MergeStrategy: "pr", PRNumber: 2, PRURL: "https://example.test/pull/2"})

	if strings.Contains(updated, "pull/1") || strings.Contains(updated, "pr: 1\n") {
		t.Errorf("stale PR reference survived rewrite:\n%s", updated)
	}
	if !strings.Contains(updated, "pr: 2") || !strings.Contains(updated, "pull/2") {
		t.Errorf("new PR reference missing:\n%s", updated)
	}
	if !strings.Contains(updated, "free text") {
		t.Errorf("prose should be preserved:\n%s", updated)
	}
}

// TestAttachmentFields_NoMR covers the machine-readable opt-out. The 2026-07-25
// incident's "no MR wisp" instruction existed only as prose, so gt done had
// nothing to obey.
func TestAttachmentFields_NoMR(t *testing.T) {
	for _, key := range []string{"no_mr", "no-mr", "nomr", "no_mr_wisp", "no-mr-wisp"} {
		fields := ParseAttachmentFields(&Issue{Description: key + ": true"})
		if fields == nil || !fields.NoMR {
			t.Errorf("%s: true should set NoMR", key)
		}
	}

	if fields := ParseAttachmentFields(&Issue{Description: "no_mr: false"}); fields == nil || fields.NoMR {
		t.Error("no_mr: false must not set NoMR")
	}
	if fields := ParseAttachmentFields(&Issue{Description: "no_merge: true"}); fields == nil || fields.NoMR {
		t.Error("no_merge must not imply no_mr — they are distinct opt-outs")
	}

	formatted := FormatAttachmentFields(&AttachmentFields{NoMR: true})
	if !strings.Contains(formatted, "no_mr: true") {
		t.Errorf("FormatAttachmentFields lost no_mr: %q", formatted)
	}
	if reparsed := ParseAttachmentFields(&Issue{Description: formatted}); reparsed == nil || !reparsed.NoMR {
		t.Error("no_mr should survive a format/parse round trip")
	}
}

func TestSetAttachmentFields_ReplacesNoMR(t *testing.T) {
	issue := &Issue{Description: "no_mr: true\ndispatched_by: mayor\n\nprose"}
	updated := SetAttachmentFields(issue, &AttachmentFields{DispatchedBy: "mayor"})
	if strings.Contains(updated, "no_mr") {
		t.Errorf("cleared no_mr should not survive rewrite:\n%s", updated)
	}
	if !strings.Contains(updated, "prose") {
		t.Errorf("prose should be preserved:\n%s", updated)
	}
}
