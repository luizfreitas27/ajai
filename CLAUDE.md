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
go build ./cmd/aja          # the binary (a pseudo-version from git; releases get the tag via -ldflags)
go test ./...               # unit tests: heuristics, config, memory, selector checks on fixtures
go test -tags browser ./... # also the end-to-end tests that launch a headless Chrome
go vet ./... && gofmt -l .  # CI runs both plus golangci-lint
goreleaser release --snapshot --clean   # all six release binaries into dist/, nothing published
```

Releases: pushing a `v*` tag runs `.github/workflows/release.yml` (goreleaser, config in
`.goreleaser.yaml`). CI's `release-snapshot` job builds the same targets on every PR.

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
  site; records runs and jobs in the store. `Runner.Session` repeats runs for
  `settings.run_non_stop` (`FilterCycle` changes the filters between them; `End.Continues`
  decides whether another run starts). `Runner.AfterFilters` is the filter pause hook.
- `internal/config/wired_test.go` fails when a config field is read nowhere outside the
  config package: a setting must do what its comment says. It type-checks the module
  (`golang.org/x/tools/go/packages`), counts reads only (an assignment, e.g. the importer's,
  is not a use) and credits a read inside a `Config` method (other than `Validate`) to its
  callers. Retire a setting through `config.RetiredKeys` (old files keep loading, with a
  warning, and error line numbers stay right), never by just deleting it.
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
- `internal/memory`, `internal/store`: answer memory and SQLite (pure Go driver). A pending
  entry with an empty answer is a question queued for the user (`aja answers review`);
  lookups treat it as a miss so the AI is still asked.
- `internal/ai`: one OpenAI-compatible client on the official openai-go SDK, the candidate
  profile and the prompt. The chat service is built without `openai.NewClient`, so the
  SDK's environment defaults (`OPENAI_API_KEY`, `OPENAI_ORG_ID`, `OPENAI_CUSTOM_HEADERS`...)
  never reach a provider. With a key the endpoint must be HTTPS or loopback.
- `internal/notify`: Telegram sender. `run.Runner` sends exactly one message per run: the
  summary, the error with the counts, or the start failure when the run could not be recorded;
  a panic is reported, then re-raised. The CLI calls `run.ReportStartFailure` when a run cannot
  start (even with a broken config file: the secrets come from the environment). The log is
  opened before the notifier is built, or its warnings would be discarded.
  The token is in the request URL, so client errors are logged without their URL.
- Help pause: `apply.Applier.Helper` (nil when unattended) is asked once per distinct set of
  stuck questions, at most `MaxHelpPauses` times per application; the form is snapshotted
  before and after, controls the user filled that were empty before are left alone from then
  on and, when the memory is consulted for them (`Decision.Remember`), captured as approved
  answers (`Answerer.Capture`). Without help, stuck questions are queued for review
  (`Answerer.RememberUnanswered`). Unattended = `--unattended`, or stdin or stdout not a
  terminal (`run.ResolveUnattended`).
- `internal/linkedin`: every LinkedIn selector, checked against `testdata/fixtures` by
  `selectors_test.go`. Update both together when LinkedIn changes.
- `internal/schedule` (F2): schedule windows and the scheduler.
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
