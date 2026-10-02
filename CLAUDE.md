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
goreleaser release --snapshot --clean   # the two Linux release binaries into dist/, nothing published
```

Releases: pushing a `v*` tag runs `.github/workflows/release.yml` (goreleaser, config in
`.goreleaser.yaml`). User-visible changes go under "Unreleased" in `CHANGELOG.md`; rename it
to the tag before tagging and the workflow publishes that section as the release notes. CI's `release-snapshot` job builds the same targets on every PR.

## Architecture (target; see issue #1 for the spec)

- `cmd/aja`: cobra command tree only. No business logic.
- `internal/config`: the YAML config, `.env` secrets, CLI overrides, defaults and validation.
  Secrets never live in the YAML.
- `internal/browser`: the `Browser`/`Page`/`Element` interfaces and the go-rod adapter
  (ADR 0001). The only package that imports rod. Never headless outside tests. `Close`
  quits Chrome gracefully on the aja profile (`Options.QuitOnClose`) and only detaches on
  the user's; never call rod's `launcher.Cleanup` on a real profile (it deletes it).
- Chrome profile: `paths.Paths.ChromeProfile(setting)` (blank = the aja profile, in the data
  directory) and `launch.OpenChrome` (find, create, open; `ErrOwnProfileInUse`) are the only
  places that decide it; `aja run` and `aja login` both go through them. `aja doctor` checks
  the LinkedIn session by opening every profile's cookie database read-only in place
  (`immutable` when Chrome holds it locked) for an unexpired `li_at`
  (`chrome.HasLinkedInSession`); never copy it, never read cookie values.
- `internal/linkedin`: `Session` over `browser.Page`: login detection (feed URL only),
  search, filters, job cards, job details, pagination; plus the centralised selectors.
- `internal/screening`: the pure skip rules (card, company, description).
- `internal/pacing`: caps and jittered pauses; `--fast` only with `--dry-run`.
- `internal/run`: the run loop over a `Site` interface (the session), tested with a fake
  site; records runs and jobs in the store. `Runner.Session` repeats runs for
  `settings.run_non_stop` (`FilterCycle` changes the filters between them; `End.Continues`
  decides whether another run starts). `Runner.AfterFilters` is the filter pause hook.
  One run at a time on the machine: `run.AcquireLock` (an OS file lock via
  `internal/filelock`, so it dies with its process and never goes stale) records the
  holder's PID, `Origin` and start time. `run.RequestStop` writes a stop-request file;
  `Lock.WatchStops` (started by `launch.Opened.Session`) polls it and cancels the session's
  context with a `*StopRequested` cause, whose reason the run records instead of "stopped
  by user". A request older than the lock (left as the previous run ended) is discarded.
- `internal/launch`: the one place a loaded configuration becomes a run: `NewPlan` (market,
  AI client, resume, Chrome; no side effects, so the caller can print it first) then
  `Plan.Open` (run lock, Chrome, session, applier, runner) and `Opened.Session`. `aja run`
  and the daemon both use it; never wire a runner anywhere else. It decides attended-only
  hooks (help pause, filter pause) from `run.Options`: the terminal always passes them,
  the daemon has none (its runs are unattended).
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
- `internal/schedule`: the pure window rules. `Window` (weekdays, `Clock` start/end in local
  wall-clock minutes, optional market and dry run), `Validate` (no crossing midnight, no
  overlap with another enabled window on the same weekday), and with an injected time
  `Current` (decided on the wall clock, as the Python bot did, so DST days behave), `Due`
  (current and not yet handled) and `Next`. The store keeps the windows
  (`schedule_windows`) and each handled opening, keyed by window and local date
  (`schedule_openings`); `aja schedule` manages them.
- `internal/service`: `aja daemon install/uninstall/status` as a systemd user unit
  (`aja.service`, `WantedBy=graphical-session.target`, `KillSignal=SIGINT` so a run ends
  as on Ctrl-C). `Unit` is golden-tested (`go test ./internal/service -update` rewrites
  `testdata/`); `systemctl --user` goes through the `Systemctl` seam. Check a changed unit
  with `systemd-analyze --user verify`.
- `internal/daemon`: what `aja daemon` runs. `Supervisor` owns the run in progress as a
  goroutine (one at a time; a second `Start` gets a `*run.HeldError`, and so does a run of
  another process, checked before the `Starter` reads any config). The CLI's `Starter`
  goes through `launch` (config read afresh, unattended forced). Panics, in opening or in
  running, never end the daemon: `Runner.Run` records and reports a crash and re-panics
  with a `*run.Crash`; anything else the supervisor reports itself and clears the live
  state. `Stop` cancels its own run with a `*run.StopRequested` cause or sends a stop
  request to another process's run; `StopIf` stops only the run a caller means, decided
  under the supervisor's lock. `Ended.Shutdown` marks a run cut short by the daemon
  stopping. `Scheduler.Tick` holds every decision for one moment (injected clock): stop our
  run when its window closed / was disabled / removed (`closedReason`), start a due
  opening, record and notify a skip when another run is in progress, but let the next
  opening wait while our own run is still ending (back-to-back windows). `RunEnded` records
  the run id, or forgets an opening the daemon's stop interrupted so the window resumes;
  `Loop` ticks every 30 s and on `RunEnded`. `OnEnd` runs before `Wait` returns, so
  shutdown never closes the store under it. The daemon lock (`daemon.AcquireLock`) allows one daemon; its log is
  `daemon-YYYY-MM-DD.log` via `logging.OpenDaily`, which moves to a new file at midnight.
- aja is a CLI and targets Linux only for now: no web panel, no Windows or macOS code paths
  in new work (the few OS branches from phase 1 stay), releases for linux/amd64 and arm64.

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
