# Fork-specific: the `automerge` path

> This file documents behaviour that exists **only on
> `blairsilverberg/gastown`**, not upstream `steveyegge/gastown`. It is a new
> file rather than an edit to an existing doc so that syncing from upstream
> never conflicts on it.

Upstream's model is that Gas Town agents **push directly to `main`** and PRs are
for external contributors — enforced by `.github/workflows/block-internal-prs.yml`,
which closes same-repo PRs.

This fork adds a second, *stricter* route for agent work: a pull request that
must attach a label, pass a guard, and go green before it lands.

## How it works

1. Open a PR against `main`.
2. Apply the **`automerge`** label. The label is the authorisation — it needs
   write access to apply, so a green build alone never merges anything. That
   matters because this repo is public and anyone can open a PR.
3. `.github/workflows/automerge.yml` then approves and squash-merges it once
   **every** check on the head commit has passed.

Labelled PRs are exempt from `block-internal-prs.yml`. Closing them would remove
checks rather than add any, since this path is stricter than pushing straight to
`main`.

## What a labelled PR may not change

`.github/workflows/automerge-guard.yml` fails an `automerge`-labelled PR that
touches any of:

| Path | Why |
|---|---|
| `**/*_test.go` | the tests themselves |
| `**/testdata/**` | fixtures the tests assert against |
| `.github/workflows/**` | the workflows that run them |
| `test_*.py`, `*_test.py`, `conftest.py` | the Python-side tests |

Otherwise an agent could make CI green by weakening what measures it rather
than fixing the code. The guard applies **only** when the label is present — a
human-reviewed PR may edit tests freely, it just takes the reviewed path.

A consequence worth knowing: a fix to a *test* cannot be landed through
automerge, by design. It needs a direct push or a reviewed PR. The agent that
needs such a fix cannot self-serve it.

## Two things `automerge.yml` deliberately does not do

**It does not use `gh pr merge --auto`.** That delegates waiting to GitHub,
which only means something when the branch has required status checks. `main`
here is unprotected (`GET /branches/main/protection` → 404), so native
auto-merge would fire immediately and merge red. The workflow reads the check
runs and the combined commit status itself.

**It does not treat "no checks" as success.** An empty check list means CI never
ran or the query broke, which is indistinguishable from "nothing failed" if you
only test for the absence of failures. It refuses instead.

It also re-checks the protected paths at merge time rather than trusting that
the guard ran — a PR can be labelled after the guard completed, and a skipped
job reports neutral rather than failure.
