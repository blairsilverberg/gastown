package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/workspace"
)

// This file implements the "at or above a town root" half of the rm -rf guard.
//
// WHY IT EXISTS: the original predicate only blocked a target spelled literally
// "/" or "/*". A town root such as /home/ubuntu/gt is a worktree shared by every
// agent in the town ALONG WITH ITS GIT INDEX, and deleting it — or any ancestor
// of it — destroys every seat at once. Those targets were allowed. Ruling by
// Blair, 2026-08-27 (AA-1019): block when the delete target resolves to a path
// at or above a town root; anything strictly BELOW a town root stays allowed.
//
// WHAT THIS CANNOT CATCH. This is a token scanner over one command string. It
// has no shell, so it cannot see through:
//   - a cwd it was never told about: `cd /elsewhere; rm -rf gt` is caught only
//     when the `cd` is in the SAME command string (we parse those) or the guard's
//     own cwd happens to be the parent.
//   - variable indirection beyond a literal assignment in the same string:
//     `export T=/home/ubuntu/gt` in an earlier turn, then `rm -rf "$T"`; also
//     `rm -rf "$(cat target.txt)"` and any parameter expansion.
//   - deleters that are not `rm`: `find /home/ubuntu/gt -delete`,
//     `find ... -exec rm -rf {} +`, `xargs rm -rf`, `rsync --delete` against an
//     empty source, `git clean -xfd` at a town root, `truncate`, a Python or Node
//     one-liner, `mv /home/ubuntu/gt /dev/null`.
//   - obfuscation: `r''m -rf /home/ubuntu/gt`, `$(echo rm) -rf ...`, base64.
// Treat it as a seatbelt against the fat-finger and the plausible-looking
// cleanup, NOT as a boundary. It is not complete and is not trying to be.

// globMetaChars are the shell glob metacharacters. A token containing one is a
// PATTERN, not a path: what a recursive delete actually walks is the deepest
// glob-free parent directory, so that parent is the target we test.
const globMetaChars = "*?["

// townRootsForGuard is indirected through a variable so tests can substitute a
// fixture instead of depending on the machine the test runs on.
var townRootsForGuard = discoverTownRoots

// protectedDeletePaths returns the set of absolute directories that must never
// be the target of a recursive delete: every town root this process can
// discover, PLUS every ancestor of each one, PLUS "/" unconditionally.
//
// Membership in this set IS the "at or above a town root" test — a path is at
// or above a town root exactly when it is that root or one of its ancestors.
// Paths below a town root are absent from the set and stay allowed.
//
// Keys are stored lowercased because the caller may hand us an already
// lowercased command (the hook entry point folds case before dispatching).
// On a case-sensitive filesystem this can only ever over-match a case variant
// of a protected path, which is the safe direction.
func protectedDeletePaths() map[string]bool {
	protected := map[string]bool{"/": true}

	addWithAncestors := func(dir string) {
		if dir == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		for _, start := range []string{filepath.Clean(abs), evalSymlinksOrEmpty(abs)} {
			p := start
			for p != "" {
				protected[strings.ToLower(p)] = true
				parent := filepath.Dir(p)
				if parent == p {
					break
				}
				p = parent
			}
		}
	}

	for _, root := range townRootsForGuard() {
		addWithAncestors(root)
	}
	return protected
}

// discoverTownRoots asks gastown itself where the town is, rather than
// hardcoding /home/ubuntu/gt. Three sources, all of them the ones the rest of
// the codebase already uses, because any one of them can be unavailable when a
// PreToolUse hook fires:
//
//  1. workspace.FindFromCwd — walks up from the process cwd to the OUTERMOST
//     mayor/town.json. This is how every other gt command finds the town, and
//     it is what a hook firing inside a rig or role seat will hit.
//  2. GT_TOWN_ROOT / GT_ROOT / GT_TOWN — set by the shell integration and the
//     session manager (see internal/cmd/rig_detect.go and the gt-proxy-server
//     config, which defaults to $GT_TOWN). These carry when cwd does not.
//  3. $HOME/gt — the documented default town location. Included unconditionally
//     so that a guard invoked from OUTSIDE any town (a /home/ubuntu/sessions
//     worktree, a bare cron shell) still protects the real town. Without this
//     fallback, discovery failure would silently mean "nothing is protected",
//     which is the failure mode this whole change exists to remove.
func discoverTownRoots() []string {
	var roots []string
	if root, err := workspace.FindFromCwd(); err == nil && root != "" {
		roots = append(roots, root)
	}
	for _, envName := range []string{"GT_TOWN_ROOT", "GT_ROOT", "GT_TOWN"} {
		if v := os.Getenv(envName); v != "" {
			roots = append(roots, v)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, "gt"))
	}
	return roots
}

func evalSymlinksOrEmpty(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return resolved
}

// unquoteToken strips balanced surrounding quotes: rm -rf "/home/ubuntu/gt".
func unquoteToken(tok string) string {
	for len(tok) >= 2 {
		first, last := tok[0], tok[len(tok)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			tok = tok[1 : len(tok)-1]
			continue
		}
		break
	}
	return tok
}

// expandHome resolves the two home spellings a literal token can carry: a
// leading ~ (rm -rf ~ IS rm -rf /home/ubuntu) and a leading $HOME / ${HOME}.
// Case-folded on the variable name because the caller may have lowercased the
// whole command before handing it to us.
func expandHome(tok string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return tok
	}
	if tok == "~" {
		return home
	}
	if strings.HasPrefix(tok, "~/") {
		return home + tok[1:]
	}
	for _, prefix := range []string{"${HOME}", "$HOME"} {
		if len(tok) >= len(prefix) && strings.EqualFold(tok[:len(prefix)], prefix) {
			rest := tok[len(prefix):]
			if rest == "" || rest[0] == '/' {
				return home + rest
			}
		}
	}
	return tok
}

// deglob truncates a glob pattern to the deepest glob-free directory prefix,
// which is the directory a recursive delete of that pattern actually walks.
//
//	/home/ubuntu/gt/*      -> /home/ubuntu/gt         (protected: blocks)
//	/home/ubuntu/gt/logs/* -> /home/ubuntu/gt/logs    (below the root: allowed)
//	/*                     -> /
//	*                      -> "" (meaning: the base directory itself)
func deglob(tok string) string {
	if !strings.ContainsAny(tok, globMetaChars) {
		return tok
	}
	segments := strings.Split(tok, "/")
	for i, seg := range segments {
		if strings.ContainsAny(seg, globMetaChars) {
			kept := strings.Join(segments[:i], "/")
			if kept == "" && strings.HasPrefix(tok, "/") {
				return "/"
			}
			return kept
		}
	}
	return tok
}

// deleteTargetCandidates turns one command token into the absolute paths that a
// recursive delete of it could walk. Returns nil for a token that cannot name a
// path (a flag, an empty token).
func deleteTargetCandidates(tok string, bases []string) []string {
	tok = unquoteToken(tok)
	if tok == "" || strings.HasPrefix(tok, "-") {
		return nil
	}
	tok = expandHome(tok)
	tok = deglob(tok)

	var candidates []string
	appendPath := func(p string) {
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		candidates = append(candidates, clean)
		if resolved := evalSymlinksOrEmpty(clean); resolved != "" && resolved != clean {
			// A symlink pointing at a town root is the same target by another
			// name, so test what it resolves to as well as what it says.
			candidates = append(candidates, resolved)
		}
	}

	if filepath.IsAbs(tok) {
		appendPath(tok)
		return candidates
	}
	for _, base := range bases {
		if tok == "" {
			// A bare glob: `rm -rf *` walks the base directory's contents.
			appendPath(base)
			continue
		}
		appendPath(filepath.Join(base, tok))
	}
	return candidates
}

func isShellVarName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// segmentSeparators are the shell operators that end one simple command.
// Splitting on them is what keeps `ls /home/ubuntu && rm -rf ./build` allowed:
// only the tokens belonging to the rm itself are treated as delete targets.
var segmentSeparators = map[string]bool{
	"&&": true, "||": true, ";": true, "|": true, "&": true, "|&": true,
	"(": true, ")": true, "{": true, "}": true, "\n": true,
}

// splitSegments breaks a token list into simple commands on shell operators,
// including operators glued to a token ("rm -rf /home/ubuntu/gt;").
func splitSegments(fields []string) [][]string {
	var segments [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			segments = append(segments, cur)
			cur = nil
		}
	}
	for _, f := range fields {
		if segmentSeparators[f] {
			flush()
			continue
		}
		f = strings.TrimLeft(f, ";&|")
		trimmed := strings.TrimRight(f, ";&|")
		if trimmed != "" {
			cur = append(cur, trimmed)
		}
		if trimmed != f || f == "" {
			flush()
		}
	}
	flush()
	return segments
}

// rmTargetIndex returns the index just past the "rm" command word in a segment,
// or -1 if the segment is not an rm invocation. "git rm" is excluded: it cannot
// reach outside the repository working tree and "git rm -r --cached ." is
// routine.
func rmTargetIndex(segment []string) int {
	for i, f := range segment {
		lf := strings.ToLower(unquoteToken(f))
		if lf != "rm" && !strings.HasSuffix(lf, "/rm") {
			continue
		}
		if i > 0 && strings.ToLower(segment[i-1]) == "git" {
			return -1
		}
		return i + 1
	}
	return -1
}

// hasRecursiveFlag reports whether any token is a recursive flag. Force is
// deliberately not required — see the comment on matchesDangerousRmRf.
// Handles -r, -R, -rf, -fr, -rvf, --recursive.
func hasRecursiveFlag(tokens []string) bool {
	for _, f := range tokens {
		lf := strings.ToLower(unquoteToken(f))
		switch {
		case lf == "--recursive":
			return true
		case strings.HasPrefix(lf, "--"):
			// Another long flag; do not scan its letters for "r".
		case strings.HasPrefix(lf, "-") && len(lf) > 1:
			if strings.Contains(lf, "r") {
				return true
			}
		}
	}
	return false
}

// assignmentValue returns the right-hand side of a literal shell assignment,
// or "". A literal assignment in the same command string is the one form of
// variable indirection this scanner can see: `T=/home/ubuntu/gt; rm -rf "$T"`.
// Indirection through a variable exported in an earlier turn, or through a
// command substitution, is invisible to it.
func assignmentValue(tok string) string {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 || !isShellVarName(tok[:eq]) {
		return ""
	}
	return unquoteToken(tok[eq+1:])
}

// matchesTownRootDelete reports the protected path that a recursive rm in this
// command would destroy, or "" if there is none.
func matchesTownRootDelete(fields []string) string {
	segments := splitSegments(fields)

	// Bases a relative target could resolve against: the guard's own cwd, plus
	// the argument of every cd/pushd in the same command string. The second
	// source is what catches `cd /home/ubuntu && rm -rf gt`.
	bases := baseDirsFromSegments(segments)

	// Extra target candidates from literal assignments anywhere in the string.
	var assigned []string
	for _, seg := range segments {
		for _, tok := range seg {
			if v := assignmentValue(unquoteToken(tok)); v != "" {
				assigned = append(assigned, v)
			}
		}
	}

	// Discovery stats the filesystem, so it is deferred until we know the
	// command actually contains a recursive rm. Every Bash tool call in every
	// session goes through this guard.
	var protected map[string]bool
	for _, seg := range segments {
		start := rmTargetIndex(seg)
		if start < 0 {
			continue
		}
		args := seg[start:]
		if !hasRecursiveFlag(args) {
			continue
		}
		if protected == nil {
			protected = protectedDeletePaths()
		}
		for _, tok := range append(append([]string{}, args...), assigned...) {
			for _, candidate := range deleteTargetCandidates(tok, bases) {
				if protected[strings.ToLower(candidate)] {
					return candidate
				}
			}
		}
	}
	return ""
}

func baseDirsFromSegments(segments [][]string) []string {
	var bases []string
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		bases = append(bases, cwd)
	}
	for _, seg := range segments {
		for i, f := range seg {
			if f != "cd" && f != "pushd" {
				continue
			}
			if i+1 >= len(seg) {
				continue
			}
			arg := expandHome(unquoteToken(seg[i+1]))
			if arg == "" || strings.HasPrefix(arg, "-") {
				continue
			}
			if filepath.IsAbs(arg) {
				bases = append(bases, filepath.Clean(arg))
			} else if cwd != "" {
				bases = append(bases, filepath.Clean(filepath.Join(cwd, arg)))
			}
		}
	}
	return bases
}
