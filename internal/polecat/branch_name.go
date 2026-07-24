package polecat

import (
	"fmt"
	"strings"
)

const (
	polecatBranchPrefix = "polecat/"

	// generatedIssueBranchSeparator is the default delimiter between the
	// issue ID and the generated suffix. It must stay inside the
	// docker-compose project-name charset ([a-z0-9_-], applied after
	// pipelines map "/" to "-"): the previous default "+" broke every
	// pipeline that derives a compose project name from the branch
	// (op-jt4a). "_" is the only safe choice that is also unambiguous —
	// "-" appears inside issue IDs, while "_" never occurs in issue IDs,
	// polecat names, or the base-36 suffix.
	generatedIssueBranchSeparator = "_"

	legacyPlusIssueBranchSeparator = "+"
	legacyIssueBranchSeparator     = "@"

	// subtaskIDSeparator is the character subtask bead IDs use between the
	// parent ID and each child ordinal (e.g. "gt-4kp9.5.5.1"). It is outside
	// the docker-compose project-name charset, so generated branches encode
	// it as "_" (op-42p9); see encodeBranchIssue / decodeBranchIssue.
	subtaskIDSeparator = "."

	// BranchDelimiterConfigKey is the rig config key that selects which
	// delimiter FormatGeneratedBranchName places between the issue ID and the
	// generated suffix. The default "_" is safe everywhere; the key remains
	// for rigs that still need the legacy "+" or "@" forms.
	BranchDelimiterConfigKey = "polecat_branch_delimiter"
)

// issueBranchSeparators lists every delimiter ParseBranchName recognizes
// between the issue ID and the generated suffix. Parsing accepts all of them
// regardless of the rig's configured delimiter, so branches created under a
// previous delimiter configuration (including in-flight "+" branches from
// before the default changed) keep resolving to the right issue. None of
// these characters can appear in raw issue IDs ([a-z0-9-] plus "." for
// subtasks) or polecat names, so the split is unambiguous — for "_" the
// split is the LAST occurrence, because encoded subtask dots also render as
// "_" (see parseIssueTail).
var issueBranchSeparators = []string{
	generatedIssueBranchSeparator,
	legacyPlusIssueBranchSeparator,
	legacyIssueBranchSeparator,
}

// legacyIssueBranchSeparators are the opt-in delimiters that never appear in
// encoded issue IDs, so their earliest occurrence in the tail is always the
// issue/suffix split.
var legacyIssueBranchSeparators = []string{
	legacyPlusIssueBranchSeparator,
	legacyIssueBranchSeparator,
}

// ValidBranchDelimiter reports whether s may be used as the configured
// branch delimiter. Only delimiters the parser recognizes are valid;
// anything else would produce branches that no longer round-trip to an
// issue ID.
func ValidBranchDelimiter(s string) bool {
	for _, sep := range issueBranchSeparators {
		if s == sep {
			return true
		}
	}
	return false
}

// encodeBranchIssue renders an issue ID for embedding in a generated branch
// name. Subtask IDs contain "." (e.g. "gt-4kp9.5.5.1"), which is outside the
// docker-compose project-name charset [a-z0-9_-] that pipelines derive from
// branch names (op-42p9), so each "." becomes "_". The encoding round-trips
// exactly because "_" cannot occur in raw issue IDs, polecat names, or the
// base-36 suffix — every "_" in a decoded issue part is an encoded ".".
func encodeBranchIssue(issue string) string {
	return strings.ReplaceAll(issue, subtaskIDSeparator, generatedIssueBranchSeparator)
}

// decodeBranchIssue is the inverse of encodeBranchIssue. ok=false means the
// encoded part cannot be a well-formed issue ID (a leading or trailing "_"
// would decode to a leading/trailing ".", which no subtask ID has).
func decodeBranchIssue(encoded string) (issue string, ok bool) {
	if encoded == "" ||
		strings.HasPrefix(encoded, generatedIssueBranchSeparator) ||
		strings.HasSuffix(encoded, generatedIssueBranchSeparator) {
		return "", false
	}
	return strings.ReplaceAll(encoded, generatedIssueBranchSeparator, subtaskIDSeparator), true
}

// BranchNameMeta is the structured identity encoded in a polecat branch name.
type BranchNameMeta struct {
	Polecat   string
	Issue     string
	Generated bool
}

// FormatGeneratedBranchName returns the canonical generated polecat branch
// using the default "_" delimiter.
func FormatGeneratedBranchName(polecatName, issue, suffix string) string {
	return FormatGeneratedBranchNameWithDelimiter(polecatName, issue, suffix, generatedIssueBranchSeparator)
}

// FormatGeneratedBranchNameWithDelimiter returns the generated polecat branch
// with the given issue/suffix delimiter. Invalid delimiters fall back to the
// default "_" so a bad config value can never produce an unparseable branch.
// Subtask dots in the issue ID are encoded as "_" regardless of delimiter so
// the branch stays docker-compose safe and still decodes to the exact issue.
func FormatGeneratedBranchNameWithDelimiter(polecatName, issue, suffix, delimiter string) string {
	if !ValidBranchDelimiter(delimiter) {
		delimiter = generatedIssueBranchSeparator
	}
	if issue != "" {
		return fmt.Sprintf("%s%s/%s%s%s", polecatBranchPrefix, polecatName, encodeBranchIssue(issue), delimiter, suffix)
	}
	return fmt.Sprintf("%s%s-%s", polecatBranchPrefix, polecatName, suffix)
}

// ParseBranchName decodes polecat branch names without guessing at dashed issue IDs.
func ParseBranchName(branch string) (BranchNameMeta, bool) {
	if !strings.HasPrefix(branch, polecatBranchPrefix) {
		return BranchNameMeta{}, false
	}

	rest := branch[len(polecatBranchPrefix):]
	if rest == "" {
		return BranchNameMeta{}, false
	}

	if slash := strings.Index(rest, "/"); slash >= 0 {
		if slash == 0 {
			return BranchNameMeta{}, false
		}
		polecatName := rest[:slash]
		issueTail := rest[slash+1:]
		if issueTail == "" || strings.Contains(issueTail, "/") {
			return BranchNameMeta{}, false
		}
		issue, generated, ok := parseIssueTail(issueTail)
		if !ok {
			return BranchNameMeta{}, false
		}
		return BranchNameMeta{Polecat: polecatName, Issue: issue, Generated: generated}, true
	}

	dash := strings.LastIndex(rest, "-")
	if dash <= 0 || dash == len(rest)-1 {
		return BranchNameMeta{}, false
	}
	return BranchNameMeta{Polecat: rest[:dash], Generated: true}, true
}

// ParseGeneratedBranchName decodes only branch names emitted by
// FormatGeneratedBranchName / FormatGeneratedBranchNameWithDelimiter and the
// legacy + and @ issue-suffix forms kept for in-flight branches.
func ParseGeneratedBranchName(branch string) (BranchNameMeta, bool) {
	meta, ok := ParseBranchName(branch)
	if !ok || !meta.Generated {
		return BranchNameMeta{}, false
	}
	return meta, true
}

// parseIssueTail splits the "<issue><delimiter><suffix>" tail of a generated
// branch. Legacy "+" and "@" delimiters never occur in encoded issue IDs or
// suffixes, so their earliest occurrence is the split. For the default "_"
// the LAST occurrence is the split: every earlier "_" is an encoded subtask
// "." (raw issue IDs, polecat names, and base-36 suffixes never contain "_",
// so this is exact). In-flight branches that embed raw subtask dots
// (e.g. "gt-4kp9.5_mk123456") keep resolving — "." passes through decoding
// untouched.
func parseIssueTail(issueTail string) (issue string, generated bool, ok bool) {
	delim := -1
	for _, sep := range legacyIssueBranchSeparators {
		if idx := strings.Index(issueTail, sep); idx >= 0 && (delim == -1 || idx < delim) {
			delim = idx
		}
	}
	if delim == -1 {
		delim = strings.LastIndex(issueTail, generatedIssueBranchSeparator)
	}
	if delim >= 0 {
		if delim == 0 || delim == len(issueTail)-1 {
			return "", false, false
		}
		issue, ok := decodeBranchIssue(issueTail[:delim])
		if !ok {
			return "", false, false
		}
		return issue, true, true
	}
	return issueTail, false, true
}
