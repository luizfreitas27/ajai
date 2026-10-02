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

Phase 1, the command-line tool, is in use; a few settings ported from the Python bot are
not wired yet (non-stop runs, pause after filters, smooth scrolling) along with `aja run
--json`, tracked in issue #16. Next:

- **F2**: `aja panel`, the web control panel served by the same binary, and `aja schedule`
  with a daemon and generated system units.
- **F3**: a full-screen terminal view, installers, self-update.

## Install

You need Google Chrome or Chromium. `aja` drives your real, visible Chrome profile; it never
runs headless and never stores your LinkedIn password.

**Release binaries.** Download the archive for your system from the
[Releases](https://github.com/luizfreitas27/aja/releases) page (Linux, macOS and Windows,
amd64 and arm64), check it against `checksums.txt`, unpack it and put `aja` on your `PATH`.
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

4. **Check.** `aja doctor` tests Chrome, your profile, the config, the secrets, the resume
   and the data directory, and prints every path in use.

5. **Dry run.** Close Chrome first (aja attaches to your profile), or point
   `settings.chrome_profile_dir` at a profile aja can own. Then:

   ```bash
   aja run --dry-run --fast
   ```

   Sign in to LinkedIn in the window if asked (once; the session persists). aja searches,
   screens each job, fills every form and discards it at the Review step, so you can see
   the answers it would send. `--fast` shortens the pauses and only works with `--dry-run`.

6. **For real.** Start small, then let the pacing limits do their job:

   ```bash
   aja run --max 1
   aja run
   ```

## Everyday commands

| Command | What it does |
|---|---|
| `aja run [--dry-run] [--max N] [--terms "a,b"] [--location X] [--market brasil]` | One run; flags override the config for this run only |
| `aja status` | The run in progress (live counters) and the last runs |
| `aja logs -f` | Follow today's log |
| `aja answers review` | Work through the pending answers: approve, edit, delete, skip |
| `aja answers list --state pending` | The review queue; `approve`, `edit`, `delete`, `clear` per id or state |
| `aja jobs list --applied --since 2026-10-01` | The job history; `--failed`, `--external`, `--skipped`, `--json` |
| `aja export --csv ./sheets` | The Python bot's two spreadsheets, from the database |
| `aja config show` / `get` / `set` / `validate` | The configuration, secrets masked |
| `aja doctor` | Everything a run needs, and every path |

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
