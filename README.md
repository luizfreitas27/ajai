# aja

A single-binary command-line tool that searches LinkedIn for jobs matching your
criteria and submits Easy Apply applications from your own machine, in your own
Chrome, with your own account. It remembers the answers it gives, lets you review
them, knows the difference between applying in Brazil and abroad, and tells you on
Telegram how a run went.

`aja` is the Go successor of the Python
[Auto_job_applier_linkedIn fork](https://github.com/luizfreitas27/Auto_job_applier_linkedIn).
The behaviour was ported, not reinvented: same selectors, same question heuristics
(English and Portuguese), same answer memory. See `NOTICE`.

## Status

Phase 1, the command-line tool, is complete. Next:

- **F2**: `aja daemon`, a systemd user service that starts and stops runs on schedule
  windows and takes commands from your Telegram bot, all managed from the command line.
- **F3**: a full-screen terminal view, installers, self-update.

## Install

You need Google Chrome or Chromium. `aja` drives a real, visible Chrome, on a profile of its
own (or yours, if you prefer); it never runs headless and never stores your LinkedIn password.

**Release binaries.** Download the archive for your system from the
[Releases](https://github.com/luizfreitas27/aja/releases) page (Linux amd64 and arm64;
`aja` targets Linux only for now), check it against `checksums.txt`, unpack it and put `aja` on your `PATH`.
While the repository is private, only its collaborators can download them.

**With Go 1.26+:**

```bash
go install github.com/luizfreitas27/aja/cmd/aja@latest
```

While the repository is private, `go install` needs `GOPRIVATE=github.com/luizfreitas27`
and git access to it.

**From source:**

```bash
git clone git@github.com:luizfreitas27/aja.git && cd aja
go build ./cmd/aja && ./aja version
```

## First run

1. **Configure.** Either answer a short wizard:

   ```bash
   aja init
   ```

   or bring everything over from the Python bot (settings, answer memory, job history,
   resume; never secrets):

   ```bash
   aja import ~/Projects/Automations/Auto_job_applier_linkedIn
   ```

2. **Resume.** Put it in the resumes directory that `aja doctor` prints (`resume.pdf` by
   default). Each market may have its own: `aja config set markets.brasil.resume cv-pt.pdf`.

3. **Secrets**, only the ones you use, in the `.env` next to the config file:

   ```bash
   AJA_AI_API_KEY=sk-...           # a hosted AI provider
   AJA_TELEGRAM_BOT_TOKEN=123:ABC  # Telegram, both lines or neither
   AJA_TELEGRAM_CHAT_ID=987654321
   ```

4. **Sign in to LinkedIn, once.** aja uses a Chrome profile of its own, so your everyday
   Chrome stays open and free while it works:

   ```bash
   aja login
   ```

   Chrome opens on LinkedIn; sign in by hand (aja never types your password) and it closes
   once the feed appears. The session stays in the aja profile.

5. **Check.** `aja doctor` tests Chrome, the profile (and whether LinkedIn is signed in
   there), the config, the secrets, the resume and the data directory, and prints every
   path in use.

6. **Dry run.**

   ```bash
   aja run --dry-run --fast
   ```

   aja searches,
   screens each job, fills every form and discards it at the Review step, so you can see
   the answers it would send. `--fast` shortens the pauses and only works with `--dry-run`.

7. **For real.** Start small, then let the pacing limits do their job:

   ```bash
   aja run --max 1
   aja run
   ```

**Coming from phase 1?** aja used to drive your everyday Chrome profile. A blank
`settings.chrome_profile_dir` now means the aja profile, one of aja's own: run `aja login` once, or set
`settings.chrome_profile_dir` to your everyday profile (`aja doctor` prints its path) to
keep the old behaviour. On the aja profile a run closes its Chrome when it ends; on yours
it leaves the window open, as before.

## Everyday commands

| Command | What it does |
|---|---|
| `aja run [--dry-run] [--max N] [--terms "a,b"] [--location X] [--market brasil]` | One run (or a non-stop session); flags override the config for this run only |
| `aja run --json` | The same, with one JSON object per line for every event and summary, for scripts |
| `aja stop` | Stop the run in progress cleanly, from any terminal (as Ctrl-C in its own would) |
| `aja status` | The run in progress (live counters, who started it) and the last runs |
| `aja logs -f` | Follow today's log |
| `aja answers review` | Work through the pending answers: approve, edit, delete, skip |
| `aja answers list --state pending` | The review queue; `approve`, `edit`, `delete`, `clear` per id or state |
| `aja jobs list --applied --since 2026-10-01` | The job history; `--failed`, `--external`, `--skipped`, `--json` |
| `aja export --csv ./sheets` | The Python bot's two spreadsheets, from the database |
| `aja config show` / `get` / `set` / `validate` | The configuration, secrets masked |
| `aja doctor` | Everything a run needs, and every path |
| `aja login` | Sign in to LinkedIn once in the profile aja uses |

Only one run happens on a machine at a time: a second `aja run` refuses and says which run
is in progress. `aja stop` asks that run to end: the job in progress is finished or
discarded, the run is recorded with "stopped with aja stop" and its summary sent.

## Answers, the AI and the help pause

Questions LinkedIn forms ask are answered from your configuration first (English and
Portuguese labels), then from the answer memory, then, if `ai.enabled: true`, by a language
model: OpenAI, DeepSeek, Gemini, or a local Ollama, LM Studio or vLLM. For dropdowns and
radio buttons the model's answer counts only when it names one of the options exactly. The
model sees a short candidate profile built from your config, never your phone, email,
address, citizenship status or current salary, and it is never asked about visas, work
authorization, citizenship, salary, disability, veteran status or clearances: those are
answered from your configuration or not at all. A key is only ever sent over HTTPS or to
localhost.

Every answer the AI gives is remembered as pending and used; `aja answers review` lets you
approve or correct it. Only an approved answer may tick a checkbox.

With you at the terminal, a question nothing can answer pauses the job: aja shows it (and
sends a Telegram message), you fill it in Chrome and press Enter, and what you typed is
remembered as approved, so the next form that asks goes through. The pause waits up to 15
minutes. With `--unattended`, or when stdin or stdout is not a terminal (a script, a
scheduler), the job is skipped instead and the question waits for your answer in
`aja answers review`.

## Non-stop runs and filter pauses

With `settings.run_non_stop: true`, a run that visited every search term (or hit the
per-run cap) is followed, ten minutes later, by another, until LinkedIn's daily limit, the
hourly cap, `--max`, an error or Ctrl-C. The filters change between runs the way the
Python bot changed them: `settings.cycle_date_posted` sets the date filter to "Past 24
hours" for every later run, and `settings.alternate_sort_by` flips the sort order before
each one (a blank sort becomes "Most relevant", then "Most recent", and so on). Each run is
recorded and reported on its own; `aja status` shows when the next one starts.

With `search.pause_after_filters: true` (the default) and you at the terminal, aja stops
after applying each term's filters so you can check or adjust them in Chrome; press Enter
to go on, or type `off` to stop pausing for the session.

## Markets

`markets.active` is `internacional` (foreign currency, annual salaries) or `brasil` (BRL,
monthly salaries). Each market may set its own currency, salaries, search location, search
terms and resume; blank values fall back to the general settings (a blank currency to BRL or
USD). Salary questions get the
figure in the period they ask for, and the market's own period when they name none.
Override per run with `aja run --market brasil`.

## Telegram

With both secrets set, aja sends one message when a run ends (the summary, or the error that
stopped it with what was done until then), one when a run cannot start (configuration,
Chrome, AI settings; not a mistyped flag), and a nudge when it waits for your login or your
help. A failed send never affects the run, and the token never
reaches the log.

To get the two values: create a bot with [@BotFather](https://t.me/BotFather) (it gives the
token), send your bot any message, then open
`https://api.telegram.org/bot<token>/getUpdates` and copy `"chat":{"id": ...}`.

## Files

Config lives in the OS config directory (`~/.config/aja/config.yaml` on Linux), with the
`.env` beside it. The SQLite database (history and answer memory, pure-Go driver), the logs,
the screenshots of failed applications and the resumes live in the OS data directory
(`~/.local/share/aja` on Linux). `AJA_HOME=<dir>` puts everything under one directory;
`--config <file>` relocates the config file alone; `AJA_CFG_SECTION_KEY=value` overrides one
key for one process without touching the file.

## Releasing

Releases are built by GitHub Actions from a tag:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

`.goreleaser.yaml` builds the six binaries with the tag as the version (`aja version`), and
publishes them with `checksums.txt`. Every pull request already builds them without
publishing, so a broken build shows up before a tag does.

## Honest note on detection

There is no mature anti-detection browser library in Go. `aja` relies instead on driving
your genuine Chrome (so every network request is Chrome's own) and on conservative pacing:
15 applications an hour and 50 a run by default, with jittered pauses between jobs and
between form actions. The residual risk and the reasoning are in
`docs/adr/0001-go-rod-on-the-users-chrome.md`. Use it as a person would: a few dozen
applications a day, not hundreds.

## License

MIT. See `LICENSE` and `NOTICE`.
