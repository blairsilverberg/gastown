package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/formula"
)

// patrolFormulas are the three patrol formulas that carried armed
// `bd mol wisp gc --force` commands in the embedded tier (op-y9xv).
var patrolFormulas = []string{
	constants.MolDeaconPatrol,
	constants.MolRefineryPatrol,
	constants.MolWitnessPatrol,
}

// scanGCCommands decodes a formula and classifies every `bd mol wisp gc`
// occurrence across all step descriptions.
//
// It counts COMMANDS, not physical lines: a TOML description may pack many
// statements into one physical line via \n escapes, so a line-anchored scan of
// the raw file sees 3 of the 5 and reports the other 2 as absent. Decoding
// first is what makes the count honest.
func scanGCCommands(t *testing.T, formulaName string) (armed, disabled int) {
	t.Helper()

	content, err := formula.GetEmbeddedFormulaContent(formulaName)
	if err != nil {
		t.Fatalf("could not read embedded formula %s: %v", formulaName, err)
	}
	f, err := formula.Parse(content)
	if err != nil {
		t.Fatalf("could not parse embedded formula %s: %v", formulaName, err)
	}
	if len(f.Steps) == 0 {
		t.Fatalf("embedded formula %s decoded to zero steps - the scan below "+
			"would report a vacuous all-clear", formulaName)
	}

	for _, step := range f.Steps {
		for _, line := range strings.Split(step.Description, "\n") {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "# DISABLED: bd mol wisp gc"):
				disabled++
			case strings.HasPrefix(trimmed, "bd mol wisp gc"):
				armed++
			}
		}
	}
	return armed, disabled
}

// TestEmbeddedPatrolFormulasCarryNoArmedWispGC pins release condition 2 of
// op-y9xv: the embedded formula tier must ship no runnable `bd mol wisp gc`.
//
// This matters because the embedded tier is what resolves on any host where
// the town tier is absent (fresh install, reprovision, .beads/formulas not yet
// written). `bd mol wisp gc` has NO owner filter - it reaps hooked, escalation
// and message wisps belonging to every role in the town.
//
// The test emits BOTH verdicts in one run: an armed count of zero is only
// admissible alongside a non-zero disabled count proving the scanner can see
// these commands at all.
func TestEmbeddedPatrolFormulasCarryNoArmedWispGC(t *testing.T) {
	totalArmed, totalDisabled := 0, 0

	for _, name := range patrolFormulas {
		armed, disabled := scanGCCommands(t, name)
		t.Logf("%-24s armed=%d disabled=%d", name, armed, disabled)
		if armed != 0 {
			t.Errorf("%s: %d armed `bd mol wisp gc` command(s) in the embedded tier; "+
				"prefix each with `# DISABLED: ` (see op-y9xv)", name, armed)
		}
		totalArmed += armed
		totalDisabled += disabled
	}

	if totalArmed != 0 {
		t.Errorf("embedded tier total armed = %d, want 0", totalArmed)
	}

	// Positive control. Without this, a scanner that silently stopped matching
	// would report a clean zero and read exactly like a genuine all-clear.
	if totalDisabled == 0 {
		t.Fatalf("scanner found no `# DISABLED: bd mol wisp gc` lines either - "+
			"it is not reading these formulas, so the armed=%d result above is "+
			"UNKNOWN, not a pass", totalArmed)
	}
}

// TestWitnessPatrolCallSiteUsesFullRenderer pins the other half of the change:
// outputWitnessPatrolContext must call the FULL renderer, not the truncating
// one. The witness role was the only patrol role left on showFormulaSteps.
//
// This is an AST check on the call site rather than a check on the renderer,
// deliberately. A test that calls renderFormulaStepsFull directly still passes
// with the call site reverted to showFormulaSteps - it exercises the renderer
// while the defect lives in the wiring - so it cannot catch this regression at
// all. Verified by reverting the call site and watching that version pass.
//
// truncateDescription keeps a step's FIRST LINE only, so raising its cap
// delivers nothing: the loss is structural, measured at ~20,641 of 21,683
// characters (~97.7%) of patrol instruction (op-y9xv).
func TestWitnessPatrolCallSiteUsesFullRenderer(t *testing.T) {
	const (
		srcFile  = "prime_molecule.go"
		funcName = "outputWitnessPatrolContext"
	)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, srcFile, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", srcFile, err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == funcName {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatalf("could not find func %s in %s - this test is anchored to a "+
			"function that no longer exists, so it proves nothing", funcName, srcFile)
	}

	calls := map[string]int{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok {
			calls[ident.Name]++
		}
		return true
	})

	if calls["showFormulaSteps"] > 0 {
		t.Errorf("%s calls the TRUNCATING renderer showFormulaSteps - the witness "+
			"then receives only the first line of each patrol step (op-y9xv)", funcName)
	}
	if calls["showFormulaStepsFull"] == 0 {
		t.Errorf("%s does not call showFormulaStepsFull; it must render full step bodies", funcName)
	}

	// Positive control: prove the AST walk actually saw this function's calls.
	// Without it, a walk that matched nothing would report a clean pass.
	if len(calls) == 0 {
		t.Fatalf("AST walk found no calls at all inside %s - the walk is not "+
			"reading the function body, so the results above are UNKNOWN", funcName)
	}
	t.Logf("%s call set: %v", funcName, calls)
}

// TestWitnessPatrolEmbeddedRenderIsFullAndDisarmed checks what the full
// renderer actually delivers from the EMBEDDED tier.
//
// townRoot is an empty temp dir, so tiers 1 and 2 miss and resolution falls
// through to the embedded formula - the same tier that resolves on a fresh
// host, which is the case this change is about.
//
// Scope note: this exercises the renderer, NOT the call site. The call site is
// pinned separately above.
func TestWitnessPatrolEmbeddedRenderIsFullAndDisarmed(t *testing.T) {
	townRoot := t.TempDir()

	rendered, err := renderFormulaStepsFull(constants.MolWitnessPatrol, townRoot, "testrig")
	if err != nil {
		t.Fatalf("renderFormulaStepsFull(%s): %v", constants.MolWitnessPatrol, err)
	}

	// A phrase from the BODY of step 1, past its first line. The truncating
	// renderer cannot emit this at any cap setting, because it discards
	// everything after the first newline before the cap is applied.
	const bodyPhrase = "SWIM LANE RULE"
	if !strings.Contains(rendered, bodyPhrase) {
		t.Errorf("rendered witness patrol does not contain %q - step bodies are "+
			"being truncated to their first line (regression of op-y9xv)", bodyPhrase)
	}

	// The full render must not deliver a runnable gc command.
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "bd mol wisp gc") {
			t.Errorf("full witness patrol render delivers an ARMED gc command: %q", trimmed)
		}
	}

	// Positive control for the loop above: prove the render actually carries
	// these lines in their disabled form, so "no armed command" is a real
	// finding about the content and not an empty render.
	if !strings.Contains(rendered, "# DISABLED: bd mol wisp gc") {
		t.Fatalf("render contains no `# DISABLED: bd mol wisp gc` line either - "+
			"the gc block is not in this render, so the armed-command check above "+
			"proved nothing (rendered %d bytes)", len(rendered))
	}

	// The first line of the gc step must not describe these owner-blind
	// commands as scoped to the reader. That sentence was the only part the
	// truncating renderer ever emitted.
	if strings.Contains(rendered, "clean up YOUR OWN wisps") {
		t.Error("witness patrol still describes `bd mol wisp gc` as scoped to the " +
			"reader's own wisps; the command has no owner filter")
	}

	// The agent-bead recipe must be the resolvable form. The bare `gt agents
	// resolve` assigns the ERROR STRING to the variable on failure, so idle:N
	// never increments and `EFFORT: reduced` can never fire.
	//
	// This lives in step 9's BODY, which means the truncating renderer never
	// delivered it and the bug was inert. Switching to the full renderer starts
	// delivering that body, so the two defects have to be fixed together: the
	// on-disk town copy was fixed on 2026-07-26 and the fix was never carried
	// back into the embedded source (hq-cmd1w).
	var resolveLines int
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(line, "YOUR_AGENT_BEAD=$(gt agents resolve") {
			continue
		}
		resolveLines++
		if !strings.Contains(line, "--json") {
			t.Errorf("agent-bead recipe uses the bare resolve form, which assigns the "+
				"error string on failure: %q", strings.TrimSpace(line))
		}
	}

	// Positive control: if the recipe is absent entirely, the loop above proved
	// nothing and this render is not the one we think it is.
	if resolveLines == 0 {
		t.Fatalf("no YOUR_AGENT_BEAD resolve line in the render at all - the check " +
			"above is vacuous, not a pass")
	}
}
