# aja

A single-binary command-line tool that searches LinkedIn for jobs matching your
criteria and submits Easy Apply applications from your own machine, in your own
Chrome, with your own account. It remembers the answers it gives, lets you review
them, knows the difference between applying in Brazil and abroad, and can notify
you on Telegram when a run ends.

`aja` is the Go successor of the Python
[Auto_job_applier_linkedIn fork](https://github.com/luizfreitas27/Auto_job_applier_linkedIn).
The behaviour was ported, not reinvented: same selectors, same question heuristics
(English and Portuguese), same answer memory. See `NOTICE`.

## Status

Pre-alpha. The plan is three usable phases, tracked as GitHub issues:

1. **F1** - the CLI: `run` (with dry run), `doctor`, `config`, `answers`, `status`,
   `import` from the Python fork, Telegram notifications, markets, pacing.
2. **F2** - `aja panel`: the web control panel served by the same binary, and
   `aja schedule` with a daemon and generated system units.
3. **F3** - TUI polish, installers, self-update.

## Requirements

- Google Chrome (or Chromium) installed. `aja` drives your real, visible Chrome
  profile; it never runs headless and never stores your LinkedIn password.
- Go 1.25+ to build from source. Release binaries will be published for Linux,
  macOS and Windows.

## Build and first run

```bash
go build ./cmd/aja && ./aja version
./aja init                      # short wizard; or pass --first-name/--last-name/--terms for scripts
./aja doctor                    # Chrome, profile, config, secrets, resume, data dir, and every path in use
./aja config set search.terms "Software Engineer, Python Developer"
./aja config show               # effective config, secrets masked
./aja import ~/Auto_job_applier_linkedIn   # settings, answer memory, job history and resume from the Python fork
./aja jobs list --failed --since 2026-10-01
./aja export --csv ./sheets     # the Python bot's two spreadsheets, from the database
```

History and the answer memory live in a SQLite database (`aja.db` in the data
directory, pure-Go driver, no cgo).

Config lives in the OS config directory (`~/.config/aja/config.yaml` on Linux);
secrets go in the `.env` beside it as `AJA_AI_API_KEY`, `AJA_TELEGRAM_BOT_TOKEN`
and `AJA_TELEGRAM_CHAT_ID`. `AJA_HOME=<dir>` puts everything under one directory;
`--config <file>` relocates the config file alone. `AJA_CFG_SECTION_KEY=value`
overrides any key for one process without touching the file.

## Honest note on detection

There is no mature anti-detection browser library in Go. `aja` relies instead on
driving your genuine Chrome (so every network request is Chrome's own) and on
conservative pacing. The residual risk and the reasoning are in
`docs/adr/0001-go-rod-on-the-users-chrome.md`. Use it as a person would: a few
dozen applications a day, not hundreds.
