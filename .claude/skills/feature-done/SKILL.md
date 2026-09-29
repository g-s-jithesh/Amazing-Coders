---
name: feature-done
description: Verify a feature (F-xx) against the project's Definition of Done — tests, coverage, layering, docs, demo path — then update docs/feature-matrix.md and docs/declarations.md. Use when the user says a feature is finished, asks to mark F-xx done, or before recording the demo.
argument-hint: "<feature id, e.g. F-08>"
---

# Feature done: $ARGUMENTS

The judges check that **every feature is traceable to code**, and that features marked "Done" have a demo timestamp. This skill is the gate. **Never mark a feature Done when a check fails.** Mark it `Partial` and list the gaps.

## 1. Locate the feature

- Find the row for `$ARGUMENTS` in `docs/feature-matrix.md`. If there is no row, stop and ask whether to add one, using the list in the root CLAUDE.md §15.
- Work out the **code paths**, in this order:
  1. the matrix row
  2. `git log --oneline --grep "$ARGUMENTS"`
  3. a grep for the feature's key terms
  4. ask the user

  Record each path as `services/<svc>/...`.

## 2. Run the checks

Run everything **scoped to the affected services**. Prefer Makefile targets. If a scoped target doesn't exist yet, run the underlying tool directly and note that a Makefile target should be added.

| # | Check | How | Pass when |
|---|-------|-----|-----------|
| 1 | Unit tests pass | Python: `cd services/<svc> && pytest -q --cov=app --cov-report=term-missing --cov-report=json:coverage.json`. Go: `cd services/<svc> && go test ./... -coverprofile=cover.out && go tool cover -func=cover.out` | all green |
| 2 | Coverage gate | read coverage for `domain/` + `application/` (Go: `internal/domain`, `internal/app`) | ≥ 80% line coverage; ≥ 90% for algorithm modules (CLAUDE.md §7) |
| 3 | Integration tests | if the feature touches Kafka, Postgres, Redis, Scylla or S3: `make test-int` (or the service's `-m integration` / `-tags=integration`) | exist **and** pass |
| 4 | Edge cases | compare against the list in root CLAUDE.md §14 and the service CLAUDE.md "Test focus" | every relevant case has a test (name them) |
| 5 | BDD scenario | `tests/bdd/features/*.feature` covers the user story | scenario exists and passes (`make test-bdd`) |
| 6 | Layering & rules | delegate to the **reviewer** subagent on the feature's diff (`git diff main...HEAD -- <paths>`) | no Blocker or Major findings |
| 7 | Lint / types | `ruff check`, `mypy --strict` / `golangci-lint run` / `npx tsc --noEmit` on the touched paths | clean |
| 8 | Contracts | if an API or event changed: OpenAPI/AsyncAPI regenerated (`make docs`), Pact tests pass, `buf breaking` passes | in sync |
| 9 | ADR | if the feature introduced a new store, broker, language or cross-service contract | an ADR exists (else run `/new-adr`) |
| 10 | Numbers | every metric the feature claims (latency, savings, MAE…) | linked to `docs/evidence/` (else run `/evidence`) |
| 11 | Demo path | a step in `docs/demo-script.md` exercises it | present |

If a check can't run (e.g. Docker isn't available for Testcontainers), mark it **Not verified**, which is not the same as passed, and say why.

## 3. Update the feature matrix

In `docs/feature-matrix.md`, update this feature's row only:

- **Status:** `Done` (all checks pass), `Partial` (anything failed or wasn't verified), or `Planned`.
- **Code path:** the primary directories, comma-separated.
- **Tests:** the test files and directories.
- **Video:** keep the existing timestamp. If there isn't one, write `TBD` (it's filled in after recording).
- **Last verified:** today's date plus the short git SHA (`git rev-parse --short HEAD`).

## 4. Update the declarations

If Claude wrote a meaningful part of this feature (check the session and the git log), add or update an entry under **AI tools used** in `docs/declarations.md`:

```
| F-xx | <component/paths> | Claude (Claude Code) | <what it did: e.g. drafted DP optimiser + unit tests; human reviewed and modified X> | <reviewer name: TBD> |
```

Also add any new third-party library the feature introduced to the **Open-source components** table (name, version, licence). Look the licence up in the package metadata; don't guess it.

## 5. Report

Reply with:

1. a verdict line: `F-xx → Done` or `F-xx → Partial (n gaps)`
2. a table of the 11 checks, each Pass / Fail / Not verified / N/A, with a one-line note
3. the gaps as a checklist, each with the exact next command or file to fix
4. the files changed (matrix, declarations)

Don't commit automatically. Suggest a commit message instead, e.g. `docs(matrix): mark F-08 done`.
