# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`ajai`: a Go single-binary CLI that applies to LinkedIn Easy Apply jobs by driving the
user's own visible Chrome. It is a port of the Python fork at
`../Auto_job_applier_linkedIn` (also on GitHub under `luizfreitas27`). When porting
behaviour, read the Python source and its tests first and translate them; do not
reinvent selectors or heuristics. The Python repo's `CLAUDE.md` and `CONTEXT.md`
describe the behaviour being ported.

## Commands

```bash
go build ./cmd/ajai         # the binary (a pseudo-version from git; releases get the tag via -ldflags)
go test ./...               # unit tests: heuristics, config, memory, selector checks on fixtures
go test -tags browser ./... # also the end-to-end tests that launch a headless Chrome
go vet ./... && gofmt -l .  # CI runs both plus golangci-lint
goreleaser release --snapshot --clean   # the two Linux release binaries into dist/, nothing published
```

Releases: pushing a `v*` tag runs `.github/workflows/release.yml` (goreleaser, config in
`.goreleaser.yaml`). User-visible changes go under "Unreleased" in `CHANGELOG.md`; rename it
to the tag before tagging and the workflow publishes that section as the release notes. CI's `release-snapshot` job builds the same targets on every PR.

## Architecture (target; see issue #1 for the spec)

- `cmd/ajai`: cobra command tree only. No business logic.
- `internal/cli`: the commands. `ajai messages send` opens Chrome only through
  `launch.Plan.OpenMessaging`, never `Plan.Open`, so it cannot search or apply. `ajai messages
  backfill` goes through it too, with `--origin web` as the web front's origin.
- `internal/config`: the YAML config, `.env` secrets, CLI overrides, defaults and validation.
  Secrets never live in the YAML.
- `internal/browser`: the `Browser`/`Page`/`Element` interfaces and the go-rod adapter
  (ADR 0001, window modes ADR 0003). The only package that imports rod. `Options.Window`
  is `settings.window`: offscreen (the default, headful but off screen), minimized (headful,
  minimized over CDP), visible, or headless (opt-in, `--headless=new`, easier for LinkedIn to
  detect). `launch.OpenChrome` passes the setting; `ajai login` always opens visible. `Close`
  quits Chrome gracefully on the ajai profile (`Options.QuitOnClose`) and only detaches on
  the user's; never call rod's `launcher.Cleanup` on a real profile (it deletes it).
  `Element.ShadowElements` searches an element's open shadow root (absolute XPath from its
  root); `ScriptClick` calls the element's `click()` in the page, for a control whose real
  click hangs (LinkedIn's hirer Message link). `Press` also takes `Shift+Enter`.
- Chrome profile: `paths.Paths.ChromeProfile(setting)` (blank = the ajai profile, in the data
  directory) and `launch.OpenChrome` (find, create, open; `ErrOwnProfileInUse`) are the only
  places that decide it; `ajai run` and `ajai login` both go through them. `ajai doctor` checks
  the LinkedIn session by opening every profile's cookie database read-only in place
  (`immutable` when Chrome holds it locked) for an unexpired `li_at`
  (`chrome.HasLinkedInSession`); never copy it, never read cookie values.
- `internal/linkedin`: `Session` over `browser.Page`: login detection (feed URL only),
  search, filters, job cards, job details, pagination; plus the centralised selectors.
  `HiringTeam` reads the job page's hirer (read only). `SendMessage` works inside the open
  shadow root of `#interop-outlet` (`ShadowElements`, `ScriptClick`), re-checks the hirer by
  normalised `/in/` URL before the send, and is free only: a Premium upsell or an InMail without
  "Free message" is `SendNoRecipient`, a `/checkpoint/` page is `SendBlocked`.
- `internal/messages`: pure rendering of a market's `message_template` (`Render`), `DetectLanguage` (stopword counts that pick the `messages.templates` language, or "" when unsure), and the
  digests an approval quotes back: `Digest` for one text, `ListDigest` for a queue listing.
  `internal/remote` approves answers with the same `Digest`.
- `internal/screening`: the pure skip rules (card, company, description).
- `internal/pacing`: caps and jittered pauses; `--fast` only with `--dry-run`.
- `internal/run`: the run loop over a `Site` interface (the session), tested with a fake
  site; records runs and jobs in the store. `Runner.Backfill` sends what applied jobs never got:
  `UnreachedApplied` lists them, `JobHiringTeam` reads each hirer, `queueHirer` queues it approved,
  and `sendMessages`/`sendInvites` send as a run's end does. `Runner.Session` repeats runs for
  `settings.run_non_stop` (`FilterCycle` changes the filters between them; `End.Continues`
  decides whether another run starts). `Runner.AfterFilters` is the filter pause hook.
  One run at a time on the machine: `run.AcquireLock` (an OS file lock via
  `internal/filelock`, so it dies with its process and never goes stale) records the
  holder's PID, `Origin` and start time. `run.RequestStop` writes a stop-request file;
  `Lock.WatchStops` (started by `launch.Opened.Session`) polls it and cancels the session's
  context with a `*StopRequested` cause, whose reason the run records instead of "stopped
  by user". A request older than the lock (left as the previous run ended) is discarded.
  With `messages.auto_send`, `queueMessage` records the message approved and sends it at once
  through `Site.SendHere` (`sendNow`: the daily cap, then `sendOne` as at the end of a run; a
  block sets `sendsStopped` for the rest of the run). One message per recruiter, ever:
  `store.HasMessaged` is checked when queueing and before each end-of-run send.
  After an applied job (never on a dry run) `Runner.queueMessage` queues its recruiter
  message from `Site.HiringTeam`. `Runner.Run` sends the approved messages once a live run's
  loop ends without an error (`sendMessages`); a cancelled context sends nothing more.
  `endAbandoned` marks the rows a dead process left `sending` as failed, never approved, so
  they are never resent.
- `internal/tui`: the TUI, on Bubble Tea v1 (the version huh brings). Bare `ajai` opens it
  only when stdin and stdout are terminals (`cli.inTerminal`, `cli.showTUI` are the test
  seams); otherwise the help, as before. `tui.New(Deps, renderer)` builds the model;
  everything outside the process goes through `Deps`. Refreshes are chained (a tick
  starts a read, its result schedules the next tick), so two never overlap and the
  Dashboard's `logging.Tail` is used by one at a time. The Dashboard follows the daemon's
  log while a run the daemon started (`Holder.ByDaemon`) holds the lock, the terminal
  runs' log while another run does, and keeps the last one while no run does. What the
  run in progress is doing comes from `run.ReadActivity`, shared with `ajai status` and
  Telegram's `/status`; never re-derive it from the lock and state file.
  Runs started from the TUI (`runControl`, `r`/`x` on the Dashboard) are
  `ajai run --unattended --origin tui` through `Deps.Launcher.Start`: a process in a session
  of its own (`Setsid`, null standard streams, the environment inherited), never a
  goroutine of the TUI. The hidden `--origin tui` makes the holder `run.OriginTUI` ("the
  TUI"). It began when its pid holds the run lock; when its process ends, a
  `run.LogStartFailure` line since its start (`run.ReadStartFailure`; `ajai run` logs one
  for every start failure, as its stderr goes nowhere) says why it did not start, even
  after it held the lock (Chrome failing to open). A run's end is announced from the run
  rows (each run of a non-stop session too); a lock let go with no run recorded since it
  was taken says the run ended before recording anything. The Dashboard refresh polls the
  process's end without blocking, so the refresh chain stays serial. Stop is a
  `run.RequestStop` with "stopped from the TUI" for the run the user confirmed (pid and
  start time), never another that took the lock meanwhile.
  Tests drive the model synchronously (`harness_test.go`: keys, sizes, explicit ticks,
  a real store with `Store.WithClock`, a temporary AJAI_HOME) and compare whole screens
  with `testdata/*.golden` (`go test ./internal/tui -update` rewrites them; the
  temporary AJAI_HOME is written as `$AJAI_HOME`). With `h.color` the harness draws in
  24-bit colour and the golden keeps the escape sequences, written `\e[…m` (the
  `colour-*` goldens pin the flame accent and the state colours; the rest stay plain).
  List tabs (Answers, Schedule, Jobs, Config, the setup checklist) describe themselves as a
  `listFrame` (`frame.go`: title, filters, summary, notes, a waiting text, optional column
  names, rows for a given width, `isTitle` rows that are section titles — never selected,
  brought into view going up — and details for a width and whether they stand beside the
  list) and the frame lays them out: details in a panel from
  `detailsBesideMinWidth` columns (the last line saying how many more when they outgrow
  the screen), else directly under the list with `minDetails` kept so the
  list keeps its height; the summary drops under the filters when they leave no room.
  The details are wrapped to their width in both places (`detailLines`), never cut.
  `listFrame.keep` keeps the selection in view, and an editor inside the details takes
  `detailWidth`; a tab's selected item tolerates a cursor past the top, which is settled
  only after the details are measured. Shared pieces: `listNotes` (a problem, else the notice), `columns` and
  `shares` for rows that split the width, `labelled` for a fact in the panel.
  The Dashboard's logo is fixed art in `logo.go`, coloured row by row from `flame` (in
  `styles.go`). `summaryLines` sets it beside the summary when the width holds the logo and
  the widest summary line (and is at least `logoBesideMinWidth`), above it otherwise, and
  in either case only if the height still leaves `belowSummary` (the last runs and
  `minLogLines` of log); else the summary goes alone. It never wraps or cuts a line.
  Colours live in `styles.go`: `accent` (flame orange, the state colours' mid tone so it
  reads on light and dark backgrounds) for the active tab, titles, the filter shown and
  key names (`styles.hints` draws the footer); `label` is a value's name, bold only.
  Keys come from one table per screen (`Model.screenKeys`: `binding`s of a key, a word or
  two for the footer, a sentence for the help screen) on top of `Model.globals` (tabs, r,
  x, s, ?, q; on the setup checklist only tabs, ? and q, its r being its own); `hints` and
  `helpView` are both made from them, so they cannot disagree. The daemon's keys are one
  table too (`daemonSection.keys`: the strip shows it, the help explains it, the Schedule
  tab acts on it; t follows `service.Status.Running`). The model handles the global keys
  before a tab sees a key (r and x never get past a missing config); Esc reaches a tab
  unless it closes the help or leaves the checklist. Lists move with `moved` and change
  their filter with `filterStep` (←→, h l), both in `frame.go`.
  `styles.mark` starts every selectable list row (the `▌` bar on the selection), and
  `Model.divider` draws the `━` stroke under the active tab's label, which marks it
  without colour too. While a tab edits a value or asks a
  question, every key but Ctrl-C goes to it (`Model.typing`). The Config tab reads and
  saves through `config.LoadFile` (the YAML alone: no environment, no `.env`), and loads with the real
  environment only to mark `AJAI_CFG_` overrides; secrets go through `config.SetDotenv`
  (single-quoted values, refusing quotes, backslashes and line breaks; 0600, atomic) and
  their values are never rendered or logged, nor is godotenv's error text (it quotes the
  file). What the daemon reads only at start is `readAtDaemonStart`.
  The setup checklist runs `doctor.Run` (with `Deps.Probe`) when the TUI opens, after
  every fix or Config save, and every `doctorEvery` refreshes, always through
  `Model.checkAgain` (one run at a time, so an older report never wins). It opens by
  itself once when `blocking` (a failed check, or no LinkedIn session), never over a value
  being typed, and blocks every tab but Config while there is no config file. It matches
  checks by `doctor.Check*` names. Fixes run ajai subcommands through
  `Deps.Launcher.InTerminal`: in production `/bin/sh` runs ajai then waits for Enter
  (`cli.afterwards`: a `trap ':' INT` keeps the shell alive through Ctrl-C, and a trap,
  unlike an ignored signal, is reset for ajai) under `tea.ExecProcess`; tests record the
  arguments. `Deps.Home` shows paths as `~/…` (tests
  set it to the temporary AJAI_HOME, which keeps the golden screens stable).
- Editing the config file: always `config.LoadFile`, then `SetChecked`, then `Write`
  (`ajai config set`, `ajai import`, the TUI). `config.Load(p, config.MapEnv(nil), nil)`
  still reads `.env` and applies its `AJAI_CFG_` lines, which would then be written into
  the file. No adaptive
  colours: they make the renderer query the terminal's background while Bubble Tea reads
  the keyboard, and the reply arrives as key presses. To try it outside tests, run it in a
  pty that answers the OSC 11 query Bubble Tea's own init sends, or termenv eats the
  first keys.
- Typed options: `questions.PickOption` (an option, or the beginning of exactly one, never a
  guess) is the one rule for an answer a person types for a select or radio: Telegram
  replies and the TUI's Answers tab. The Answers tab approves only the answer it showed
  (re-read first), as `/pending`'s digest does.
- The daemon's systemd unit in the TUI is `Deps.Unit`: in production `cli.daemonUnit`,
  `ajai daemon install`'s own checks (`App.installCheck`; `App.handStarted` also keeps Start
  and Stop off a daemon started by hand) and options (`App.installOptions`) over
  `service.Manager`, made on each call (making it asks systemd); `Manager.Start` waits to
  see the daemon stay up, as Install does. Tests use a real `service.Manager` over the
  fake systemctl, and the CLI harness replaces systemctl with one that fails, so no test
  reaches this machine's systemd. The unit's state is read when the Schedule tab is shown
  and every `unitEvery` refreshes, one read at a time, never every second.
- The Jobs tab reads `store.ListJobs` a page (`jobsPage`) at a time (`JobFilter.Offset`;
  `JobFilter.Text` matches title, company and location through `ajai_contains`, a SQLite
  function in Go that folds case in Unicode, so "SÃO" finds "São Paulo" and `%`/`_` are
  plain text). One read at a time, tagged with the filter it was made for; a page leaves out
  jobs already shown; every `jobsEvery` refreshes it reads every job down to the oldest shown
  (`Since`), so none falls off and the selection follows its job id; a failed read keeps the
  list. Links open through `Deps.OpenURL` (`xdg-open` in its own session, not waited for, its
  output on the null device), http and https only.
- The Schedule tab reads each window's latest opening (`store.LastOpening`) and the run it
  started (`store.GetRun`) with the windows, and its next openings from
  `schedule.Upcoming`; the daemon's state, keys and questions are notes under its title
  (`daemonSection.strip`; a question takes the keys' line). `runCounts` and `runEnding`
  write a recorded run the same way on the Dashboard, the run notices and the Schedule.
  `wrapStyled` breaks a word longer than the width (a link) and `detailLines` keeps an
  indented line's indent when it wraps.
- Schedule windows are added and turned on through `store.AddValidWindow` and
  `store.SetWindowEnabledValid` (schedule.Validate against the windows there are, in one
  place): `ajai schedule` and the TUI's Schedule tab both use them.
- Lock readers: `filelock.Held` takes the lock for an instant, so another reader can see it
  taken with no description; `filelock.ReadHeld` looks again a few times before believing
  an undescribed holder (otherwise a once-a-second reader like the TUI makes the daemon
  skip schedule openings).
- `internal/launch`: the one place a loaded configuration becomes a run: `NewPlan` (market,
  AI client, resume, Chrome; no side effects, so the caller can print it first) then
  `Plan.Open` (run lock, Chrome, session, applier, runner) and `Opened.Session`. `ajai run`
  and the daemon both use it; never wire a runner anywhere else. `Plan.OpenMessaging` is its one
  other entry: the run lock (`run.OriginMessages`), Chrome and a session with no applier, for
  `ajai messages send` (`Runner.SendPending`). It decides attended-only
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
  questions are gated here. Skill years (`skills.go`, `questions.skill_years` parsed once in
  `New` into `config.Skill`) are configured answers. They are read only from a label's
  asked sentence, without its links: how long a listed skill was used gets its fewest years
  (`yearsFromSkills`), and a Yes/No about experience gets yes (`yesFromSkills`), never no,
  only when a years floor in it is reached. Their term lists live in `terms.go`.
  `internal/textmatch` is the whole-word matcher every label test uses (`HasFolded` also
  ignores accents), plus `Normalise` and the difflib `Ratio`.
- `internal/apply`: the Easy Apply modal loop. `ReadForm` reads the page's controls into
  `questions.Question` values with their DOM handles; `Applier.Apply` clicks the button,
  answers page by page, uploads the resume once, walks Next/Review/Submit, discards on a
  dry run or on stalled unanswered questions, screenshots failures. Unit-tested with a fake
  DOM (`fake_test.go`); `e2e_browser_test.go` (build tag `browser`) drives the captured
  modal fixture through the rod adapter in a headless Chrome.
- `internal/memory`, `internal/store`: answer memory and SQLite (pure Go driver). A pending
  entry with an empty answer is a question queued for the user (`ajai answers review`);
  lookups treat it as a miss so the AI is still asked. Select and radio entries keep the
  options the form offered (`questions.Choices`: placeholders out), so an answer given later
  can be checked; entries from before migration 0003 have none. `recruiter_messages`
  (migration 0004) moves queued, approved, sending, sent, failed or no_recipient;
  `ApproveMessage` and `ApproveAll` take the digest the user read and refuse a changed text or
  queue, and `MarkSending` is written before the send click.
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
  `selectors_test.go`. Update both together when LinkedIn changes. `SendMessage` (opens the
  job page) and `SendHere` (the search pane shown) share `composeAndSend` after re-reading
  the hirer.
- `internal/schedule`: the pure window rules. `Window` (weekdays, `Clock` start/end in local
  wall-clock minutes, optional market and dry run), `Validate` (no crossing midnight, no
  overlap with another enabled window on the same weekday), and with an injected time
  `Current` (decided on the wall clock, as the Python bot did, so DST days behave), `Due`
  (current and not yet handled) and `Next`. The store keeps the windows
  (`schedule_windows`) and each handled opening, keyed by window and local date
  (`schedule_openings`); `ajai schedule` manages them.
- `internal/service`: `ajai daemon install/uninstall/status` as a systemd user unit
  (`ajai.service`, `WantedBy=graphical-session.target`; `KillSignal=SIGINT` with
  `KillMode=mixed`, so only the daemon gets the signal and closes its own Chrome, as on
  Ctrl-C; `RestartPreventExitStatus` for a second daemon). The unit carries `AJAI_HOME`, the
  XDG directories and `--config` from the installing shell; `UnitDir` follows systemd's own
  `XDG_CONFIG_HOME`. `Unit` is golden-tested (`go test ./internal/service -update`
  rewrites `testdata/`); `systemctl --user` goes through the `Systemctl` seam (stdout only;
  stderr in the error). Check a changed unit with `systemd-analyze --user verify`.
- `internal/remote`: the remote commands. `Listener.Loop` long-polls `notify.Telegram.Updates`
  (the `Bot` interface), acts only on the configured (numeric) chat, others logged once.
  The update offset is saved per bot, with its date, *before* acting (a restart never
  repeats a command; a failed save skips the command; an offset older than 6 days is dropped
  because Telegram renumbers after a quiet week). Only the first batch after starting or
  reconnecting is "backlog": there a `/run` or `/stop` older than `MaxAge` is refused and a
  `/stop` never hits a run that started after it; live messages are never judged by their
  date (clock skew). 401 ends the loop, 409 is said once (also on Telegram), other errors
  back off with one warning. A panic in a command, button or reply is reported, not fatal.
  Commands go through the `Runs` interface (the daemon's `Supervisor`). `/pending` sends
  one message per pending answer (at most `pendingShown`; a 429 is waited out once) with
  inline buttons whose callback data is `approve:<id>:<digest of the answer shown>` (a
  changed answer is shown again, never approved unseen) / `delete:<id>`, and records
  (bot, chat, message) → answer id in `telegram_messages`, which is how a reply finds its
  answer after a restart (links expire after `linkMaxAge`). A reply to a select or radio
  must be an option or the beginning of exactly one (`questions.PickOption`), never a guess. Every
  outcome edits the message. `getUpdates` asks for `message` and `callback_query`
  (Telegram remembers that list). Tests run against an httptest fake of the Bot API with a
  real store and answer memory.
- `internal/daemon`: what `ajai daemon` runs. `Supervisor` owns the run in progress as a
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
- ajai is a CLI and targets Linux only for now: no web panel, no Windows or macOS code paths
  in new work (the few OS branches from phase 1 stay), releases for linux/amd64 and arm64.

## Conventions

- Standard Go style; `gofmt`, `go vet` and golangci-lint are the linters. CI formats with the
  Go version in `go.mod`; a newer local Go may align long map keys differently, so check with
  `$(GOTOOLCHAIN=go$(sed -n 's/^go //p' go.mod) go env GOROOT)/bin/gofmt -l .` before pushing.
- Exported identifiers get doc comments. Tests are table-driven where there is a table.
- Tests that need a real Chrome go behind `//go:build browser`.
- CLI and log text is English. Question heuristics match English and Portuguese labels.

## Agent skills

Issue tracker: GitHub Issues on `luizfreitas27/ajai` (`docs/agents/issue-tracker.md`).
Triage labels: the five defaults (`docs/agents/triage-labels.md`).
Domain docs: `CONTEXT.md` and `docs/adr/` (`docs/agents/domain.md`). Read them before exploring.
