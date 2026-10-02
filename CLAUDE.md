# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`aja`: a Go single-binary CLI that applies to LinkedIn Easy Apply jobs by driving the
user's own visible Chrome. It is a port of the Python fork at
`../Auto_job_applier_linkedIn` (also on GitHub under `luizfreitas27`). When porting
behaviour, read the Python source and its tests first and translate them; do not
reinvent selectors or heuristics. The Python repo's `CLAUDE.md` and `CONTEXT.md`
describe the behaviour being ported.

## Commands

```bash
go build ./cmd/aja          # the binary
go test ./...               # unit tests: heuristics, config, memory, selector checks on fixtures
go test -tags browser ./... # also the end-to-end tests that launch a headless Chrome
go vet ./... && gofmt -l .  # CI runs both plus golangci-lint
```

## Architecture (target; see issue #1 for the spec)

- `cmd/aja`: cobra command tree only. No business logic.
- `internal/config`: the YAML config, `.env` secrets, CLI overrides, defaults and validation.
  Secrets never live in the YAML.
- `internal/browser`: the `Browser`/`Page`/`Element` interfaces and the go-rod adapter
  attached to the user's Chrome profile (ADR 0001). The only package that imports rod.
  Never headless outside tests.
- `internal/linkedin`: `Session` over `browser.Page`: login detection (feed URL only),
  search, filters, job cards, job details, pagination; plus the centralised selectors.
- `internal/screening`: the pure skip rules (card, company, description).
- `internal/pacing`: caps and jittered pauses; `--fast` only with `--dry-run`.
- `internal/run`: the run loop over a `Site` interface (the session), tested with a fake
  site; records runs and jobs in the store.
- `internal/questions`: the question heuristics (`Answerer.Answer(Question, Job) Decision`),
  pure: configured answers, then the answer memory, then the AI, never a guess. Sensitive
  questions are gated here. `internal/textmatch` is the whole-word matcher every label
  test uses, plus `Normalise` and the difflib `Ratio`.
- `internal/apply`: the Easy Apply modal loop. `ReadForm` reads the page's controls into
  `questions.Question` values with their DOM handles; `Applier.Apply` clicks the button,
  answers page by page, uploads the resume once, walks Next/Review/Submit, discards on a
  dry run or on stalled unanswered questions, screenshots failures. Unit-tested with a fake
  DOM (`fake_test.go`); `e2e_browser_test.go` (build tag `browser`) drives the captured
  modal fixture through the rod adapter in a headless Chrome.
- `internal/memory`, `internal/store`: answer memory and SQLite (pure Go driver).
- `internal/linkedin`: every LinkedIn selector, checked against `testdata/fixtures` by
  `selectors_test.go`. Update both together when LinkedIn changes.
- `internal/ai`: one OpenAI-compatible client for every provider.
- `internal/notify`, `internal/schedule`, `internal/markets`, `internal/pacing`.
- `internal/panel` (F2): embedded web panel with the same security rules as the Python one:
  loopback only, no CORS, a required request header on mutations, masked secrets.

## Conventions

- Standard Go style; `gofmt`, `go vet` and golangci-lint are the linters. CI formats with the
  Go version in `go.mod`; a newer local Go may align long map keys differently, so check with
  `$(GOTOOLCHAIN=go$(sed -n 's/^go //p' go.mod) go env GOROOT)/bin/gofmt -l .` before pushing.
- Exported identifiers get doc comments. Tests are table-driven where there is a table.
- Tests that need a real Chrome go behind `//go:build browser`.
- CLI and log text is English. Question heuristics match English and Portuguese labels.

## Agent skills

Issue tracker: GitHub Issues on `luizfreitas27/aja` (`docs/agents/issue-tracker.md`).
Triage labels: the five defaults (`docs/agents/triage-labels.md`).
Domain docs: `CONTEXT.md` and `docs/adr/` (`docs/agents/domain.md`). Read them before exploring.
