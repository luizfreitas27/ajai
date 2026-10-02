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
| `aja logs -f` | Follow today's log (`--daemon`: the daemon's) |
| `aja answers review` | Work through the pending answers: approve, edit, delete, skip |
| `aja answers list --state pending` | The review queue; `approve`, `edit`, `delete`, `clear` per id or state |
| `aja jobs list --applied --since 2026-10-01` | The job history; `--failed`, `--external`, `--skipped`, `--json` |
| `aja export --csv ./sheets` | The Python bot's two spreadsheets, from the database |
| `aja config show` / `get` / `set` / `validate` | The configuration, secrets masked |
| `aja doctor` | Everything a run needs, and every path |
| `aja login` | Sign in to LinkedIn once in the profile aja uses |
| `aja schedule add/list/rm/enable/disable` | Schedule windows (see below) |
| `aja daemon` | Start and stop runs on the schedule windows, until Ctrl-C |
| `aja daemon install` / `status` / `uninstall` | The daemon from a systemd user unit |

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

## Schedule windows

A schedule window is a span of local time on chosen weekdays, optionally with its own market
and dry run:

```bash
aja schedule add --days mon-fri --start 09:00 --end 11:00 --market brasil
aja schedule add --days sat --start 10:00 --end 12:00 --market internacional --dry-run
aja schedule list            # every window, and the next opening
aja schedule disable 2       # enable, rm work the same way
```

When a window opens, one unattended run starts (a non-stop session with
`settings.run_non_stop`); when it closes, the run is stopped. A run that ends early is not
restarted in the same window, and a window that opens while another run is in progress is
skipped. Windows never cross midnight: split one in two (`22:00`-`24:00` and `00:00`-`02:00`
the next day). `aja status` shows the next opening.

### The daemon

Windows are acted on by `aja daemon`, which runs until Ctrl-C:

```bash
aja daemon install       # a systemd user unit that starts with your desktop session
aja daemon status        # installed, running?
aja logs --daemon -f     # what it decides, and the runs it starts
aja daemon uninstall     # stop it and remove the unit
```

`aja daemon install` writes `~/.config/systemd/user/aja.service` for the binary you ran it
with (carrying your `AJA_HOME`, XDG directories and `--config`, if you use them), enables it
for your desktop session, starts it and checks it stays up; run it again after moving or
upgrading aja. It warns when your session does not reach `graphical-session.target` or
systemd does not know your display, either of which would keep Chrome from opening, and
refuses while an `aja daemon` you started by hand is running. Stopping the unit is Ctrl-C:
the run in progress ends cleanly and its window resumes when the daemon is back. On your own
Chrome profile (`settings.chrome_profile_dir`), the window a daemon run opened closes with
the unit. You can also just run `aja daemon` in a terminal you leave open.

Every 30 seconds it looks at the windows: when one opens it starts an unattended run with
that window's market and dry run (a non-stop session if `settings.run_non_stop` is on) and
tells you on Telegram; when the window closes, or you disable or remove it, the run ends
cleanly with that reason. A run that ends early is not restarted in the same window. A
window that opens while another run is in progress (one you started in a terminal, say) is
skipped, and Telegram says so; two windows back to back hand over instead. The
configuration is read afresh for every run, so `aja config set` needs no restart. Only one
daemon runs at a time, and Ctrl-C lets the run in progress end cleanly first. Its runs use
the aja profile, so sign in once with `aja login` before the first window.

## Remote commands (Telegram)

With `AJA_TELEGRAM_BOT_TOKEN` and `AJA_TELEGRAM_CHAT_ID` set (the same bot that sends the run
summaries; the chat id is the numeric one), `aja daemon` also takes commands from that chat:

| Command | What it does |
|---|---|
| `/status` | The run in progress (who started it, market, counts, term) or the last run, pending answers, the next window |
| `/stop` | Stop the run in progress, wherever it was started; a second message says when it has ended |
| `/run [brasil\|internacional] [dry]` | Start an unattended run now, or say why not (a run in progress, a config error, the Chrome profile in use) |
| `/pending` | Review the answer memory's pending answers from your phone (below) |
| `/help` | The list |

`/pending` sends one message per pending answer, the 10 newest at a time: the question, its
kind, the options it offered (select and radio), the current answer and who gave it, and the
job it came from. **Approve** keeps the answer, **Delete** forgets it; a question with no
answer yet has only Delete. To give or correct an answer, reply to its message with the text:
for a select or radio it must name one of the options (case does not matter; the option's own
text is saved), otherwise the reply is refused with the options listed. A checkbox takes only
the buttons: approving it ticks it from now on. Each button or reply edits the message to show
what was done, and replies and buttons keep working after the daemon restarts. Questions
remembered before this version have no options saved, so any reply is taken for them.

Messages from any other chat are ignored. The daemon asks Telegram for new messages (long
polling), so nothing on your machine is reachable from the internet, and it remembers what
it read, so a restart never repeats a command. A `/run` or `/stop` that waited more than 10
minutes while the daemon was down is not acted on (send it again), and a waiting `/stop`
never stops a run that started after it; `/status` and `/help` are always answered. If your
bot has a webhook set, or another program reads it, Telegram hands the messages to that
one: the daemon says so once, on Telegram too (remove the webhook with `deleteWebhook`). A
revoked token stops the remote commands, with a line in the daemon's log. The token and chat
id are read when the daemon starts.

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
