# ajai

```text
  ▄███████         ▄█    ▄███████   ▄█
 ███   ███        ███   ███   ███  ███
 ███   ███        ███   ███   ███  ███
▄███▄▄▄███        ███  ▄███▄▄▄███  ███
▀███▀▀▀███        ███  ▀███▀▀▀███  ███
 ███   ███  ▄██   ███   ███   ███  ███
 ███   ███  ███   ███   ███   ███  ███
 ███   █▀    ▀█████▀    ███   █▀   █▀
```

A single-binary command-line tool that searches LinkedIn for jobs matching your
criteria and submits Easy Apply applications from your own machine, in your own
Chrome, with your own account. It remembers the answers it gives, lets you review
them, knows the difference between applying in Brazil and abroad, and tells you on
Telegram how a run went.

`ajai` is the Go successor of the Python
[Auto_job_applier_linkedIn fork](https://github.com/luizfreitas27/Auto_job_applier_linkedIn).
The behaviour was ported, not reinvented: same selectors, same question heuristics
(English and Portuguese), same answer memory. See `NOTICE`.

## Status

Phase 1, the command-line tool, is complete. Next:

- **F2**: `ajai daemon`, a systemd user service that starts and stops runs on schedule
  windows and takes commands from your Telegram bot, all managed from the command line.
- **F3**: a full-screen terminal view, installers, self-update.

## Install

You need Google Chrome or Chromium. `ajai` drives a real, visible Chrome, on a profile of its
own (or yours, if you prefer); it never runs headless and never stores your LinkedIn password.

**Release binaries.** Download the archive for your system from the
[Releases](https://github.com/luizfreitas27/ajai/releases) page (Linux amd64 and arm64;
`ajai` targets Linux only for now), check it against `checksums.txt`, unpack it and put `ajai` on your `PATH`.
While the repository is private, only its collaborators can download them.

**With Go 1.26+:**

```bash
go install github.com/luizfreitas27/ajai/cmd/ajai@latest
```

While the repository is private, `go install` needs `GOPRIVATE=github.com/luizfreitas27`
and git access to it.

**From source:**

```bash
git clone git@github.com:luizfreitas27/ajai.git && cd ajai
go build ./cmd/ajai && ./ajai version
```

## First run

The easiest way: type `ajai`. With nothing set up yet it opens on the **setup checklist**
(see [The TUI](#the-tui)), which walks you through the steps below, each one a key away.
By hand:

1. **Configure.** Either answer a short wizard:

   ```bash
   ajai init
   ```

   or bring everything over from the Python bot (settings, answer memory, job history,
   resume; never secrets):

   ```bash
   ajai import ~/Projects/Automations/Auto_job_applier_linkedIn
   ```

2. **Resume.** Put it in the resumes directory that `ajai doctor` prints (`resume.pdf` by
   default). Each market may have its own: `ajai config set markets.brasil.resume cv-pt.pdf`.

3. **Secrets**, only the ones you use, in the `.env` next to the config file:

   ```bash
   AJAI_AI_API_KEY=sk-...           # a hosted AI provider
   AJAI_TELEGRAM_BOT_TOKEN=123:ABC  # Telegram, both lines or neither
   AJAI_TELEGRAM_CHAT_ID=987654321
   ```

4. **Sign in to LinkedIn, once.** ajai uses a Chrome profile of its own, so your everyday
   Chrome stays open and free while it works:

   ```bash
   ajai login
   ```

   Chrome opens on LinkedIn; sign in by hand (ajai never types your password) and it closes
   once the feed appears. The session stays in the ajai profile.

5. **Your profile.** Set `questions.summary`, `questions.about` and `questions.skill_years`
   (see [Your profile](#your-profile) and [Skill years](#skill-years)). They answer the
   questions about your experience, and give the AI something to work from if you turn it
   on.

6. **Check.** `ajai doctor` tests Chrome, the profile (and whether LinkedIn is signed in
   there), the config, the secrets, the resume and the data directory, and prints every
   path in use.

7. **Dry run.**

   ```bash
   ajai run --dry-run --fast
   ```

   ajai searches,
   screens each job, fills every form and discards it at the Review step, so you can see
   the answers it would send. `--fast` shortens the pauses and only works with `--dry-run`.

8. **For real.** Start small, then let the pacing limits do their job:

   ```bash
   ajai run --max 1
   ajai run
   ```

**Coming from phase 1?** Phase 1 drove your everyday Chrome profile. A blank
`settings.chrome_profile_dir` now means the ajai profile, one of ajai's own: run `ajai login` once, or set
`settings.chrome_profile_dir` to your everyday profile (`ajai doctor` prints its path) to
keep the old behaviour. On the ajai profile a run closes its Chrome when it ends; on yours
it leaves the window open, as before.

## The TUI

Type `ajai` with no command, in a terminal, and it opens a full-screen interface. Outside a
terminal (a pipe, a script, systemd) it prints the help instead, and every command below
works exactly as without the TUI.

The list tabs (Answers, Jobs, Config and the setup checklist) share one layout: a header with the tab's title, every filter it has (the one
shown in brackets) and a summary; the list, the selected row marked; and the selected item's
details, in a panel to the right of the list on a terminal 120 columns wide or more, or
directly under the list on a narrower one.

Flame orange marks what is active: the tab you are on (with a thick stroke under its name,
which shows even with `--no-color`), the selected row (a `▌` bar), titles and keys. Green,
amber and red keep meaning ok, warning and failure.

The **Dashboard** greets you with the ajai logo shown at the top of this page, in a flame
gradient (yellow at the base, red at the tips). It stands beside the summary when the
terminal is wide enough for both, goes over the summary when the terminal is narrow but
tall, and steps aside rather than cut a line or take the room of the runs and the log.
It refreshes every second. It shows:
- the run in progress: who started it (the terminal, a schedule window, Telegram), the
  market, dry run or live, the term being searched and the counts so far;
- the last runs and why each ended;
- the pending answers and the next schedule window;
- whether the daemon runs;
- today's log as it grows. A run the daemon started is followed in the daemon's log.

`r`, from any tab, starts a run on the Dashboard. You choose the market (the config's, brasil or
internacional), dry run (the default) or live, and a maximum of applications; live shows in
red, and Enter on it asks once more, since that run submits applications for real. It runs
`ajai run --unattended` in a process of its own, so closing the TUI never ends it, and
questions nothing can answer go to the review queue. If a run is already in progress, Start
says which one and starts nothing; if the new run cannot start (a config problem, Chrome
missing or in use), the TUI says why. `x` stops the run in progress, whoever started it
(the terminal, the schedule, Telegram or the TUI), after asking: it ends once the job in hand
is done, recorded as "stopped from the TUI". When a run ends while the TUI is open, a line
says how it went.

The **Answers** tab is the answer memory, the review queue first (its tab title counts the
pending answers) and the approved ones a ←→ away; it keeps up as runs queue questions and
Telegram answers them. Each answer shows its question, kind,
options, who gave it, how often it was used and the last job. `a` approves it as shown (if a
run answered it again meanwhile, nothing is approved and the new answer is shown), Enter
corrects it: a select or radio is picked from its options with ↑↓, or typed (an option, or
the beginning of just one; the pick follows what you type), anything else typed; a correction is yours and approved. A checkbox
takes only `a` or `d`; `d` deletes after asking, and ajai asks the question again next time.

The **Schedule** tab lists the schedule windows (weekdays, hours, market, mode, on or off)
with each one's next opening. `n` adds one in a form: tick the weekdays with Space, type
the start and end (HH:MM, local time), choose the market and dry run or live (live shows in
red, and adding a live window asks once more: its runs submit applications). It is checked
as `ajai schedule add` checks it (no window crossing midnight, none overlapping another on
the same weekday) and refused with the reason. Space turns the selected window off or back
on (on again only if nothing overlaps it now), and `d` deletes it after asking. The selected
window's details show its next three openings and what its last opening did: the run it
started, with that run's counts and why it ended, or why no run started. Windows open
runs only while the daemon runs: a strip under the tab's title says whether it does and how
its systemd unit stands, and `i` installs (or reinstalls) the unit as `ajai daemon install` does,
`u` uninstalls it, and `t` starts the daemon (or stops it while its unit runs), each after a question that says
what it does. Installing warns when it could not start the daemon (no desktop session) or
when reinstalling ends a run in progress.

The **Jobs** tab is the job history, newest first, with the counts per outcome. ←→ show
every job or only the applied, external, failed or skipped ones; `/` searches titles,
companies and locations, ignoring case, in accented letters too (Esc clears the search). The
selected job shows its outcome and why, when it was recorded and posted, a failure's error
and screenshot, its links and the questions answered. Enter opens it in your default browser:
the company's application page for an external job, LinkedIn's otherwise (only web links
are opened). The list is read a page at a time as you scroll, and keeps up while a run
records jobs.

The **Config** tab lists every setting by section, with its value in the config file and
what it does. Enter edits one:
- a setting with fixed values is picked from a list (several, with Space, for a list such
  as `search.on_site`);
- anything else is typed, and refused with the reason if it is not valid;
- a setting your environment or `.env` overrides (`AJAI_CFG_...`) is marked, and an edit
  changes the file, never saving the override.

The three secrets (`AJAI_AI_API_KEY`, `AJAI_TELEGRAM_BOT_TOKEN`, `AJAI_TELEGRAM_CHAT_ID`) show
only whether they are set, and where (your environment or `.env`). Each one is typed into a
hidden field and saved in ajai's `.env`, readable only by you; an empty value removes it. If
the daemon runs, the TUI tells you when it needs a restart: it reads the Telegram settings
when it starts, while settings take effect on its next run.

The **setup checklist** lists every check `ajai doctor` makes, with what fixes it. The TUI
opens on it when something a run needs is missing (a check failed, or LinkedIn is not
signed in in the ajai profile); `s` opens it at any time, from any tab, and the Dashboard
says whether every check passes (they run again every half minute and after every
change). Enter fixes the selected check:
- no config file: the init wizard (`ajai init`);
- no LinkedIn session in the ajai profile: `ajai login`, which opens Chrome to sign in;
- the AI key, Telegram or the resume: the Config tab.

`i` imports from the Python bot after asking for its folder (`ajai import`), and `r` checks
again. The wizard, the sign-in and the import run in the same terminal while the TUI steps
aside (with the same `--config` and `--no-color`); press Enter at the end to come back,
and the checks run again. Without a config file
only the checklist and the Config tab (for the secrets) open.

Keys mean the same everywhere. The number keys, Tab and Shift-Tab switch tabs; ↑↓ (k j),
PgUp PgDn and g G move in a list; ←→ (h l) change its filter; Enter does the selected item's
main thing (edit it, or open the job); `r` starts a run, `x` stops it and `s` opens the setup
checklist from any tab; each letter means one thing (`n` new, `d` delete, `a` approve, Space
on or off, `/` search). The footer shows the keys of the screen shown and `?` lists them in
full; `q` or Ctrl-C quits. Quitting
never stops a run. The TUI needs a terminal of at least 80×24, honours `--no-color` and
`NO_COLOR`, and writes paths under your home as `~/…`.

## Everyday commands

| Command | What it does |
|---|---|
| `ajai run [--dry-run] [--max N] [--terms "a,b"] [--location X] [--market brasil]` | One run (or a non-stop session); flags override the config for this run only |
| `ajai run --json` | The same, with one JSON object per line for every event and summary, for scripts |
| `ajai stop` | Stop the run in progress cleanly, from any terminal (as Ctrl-C in its own would) |
| `ajai status` | The run in progress (live counters, who started it) and the last runs |
| `ajai logs -f` | Follow today's log (`--daemon`: the daemon's) |
| `ajai answers review` | Work through the pending answers: approve, edit, delete, skip |
| `ajai answers list --state pending` | The review queue; `approve`, `edit`, `delete`, `clear` per id or state |
| `ajai jobs list --applied --since 2026-10-01` | The job history; `--failed`, `--external`, `--skipped`, `--json` |
| `ajai export --csv ./sheets` | The Python bot's two spreadsheets, from the database |
| `ajai config show` / `get` / `set` / `validate` | The configuration, secrets masked |
| `ajai doctor` | Everything a run needs, and every path |
| `ajai login` | Sign in to LinkedIn once in the profile ajai uses |
| `ajai schedule add/list/rm/enable/disable` | Schedule windows (see below) |
| `ajai daemon` | Start and stop runs on the schedule windows, until Ctrl-C |
| `ajai daemon install` / `status` / `uninstall` | The daemon from a systemd user unit |

Only one run happens on a machine at a time: a second `ajai run` refuses and says which run
is in progress. `ajai stop` asks that run to end: the job in progress is finished or
discarded, the run is recorded with "stopped with ajai stop" and its summary sent.

## Answers, the AI and the help pause

Every question in an Easy Apply form is answered by the first of these that can (English
and Portuguese labels alike):

1. **Your configuration.** Name, phone, city, work authorization, visa, salary, notice period
   and total years of experience. Also your [skill years](#skill-years), for questions about
   one technology.
2. **The answer memory.** What you approved or typed before, and what the AI answered before.
3. **The AI**, only with `ai.enabled: true`.
4. **Nothing.** The job is skipped and the question goes to the review queue, or, with you
   at the terminal, to a help pause (below).

### What the AI does, and what it never does

The AI answers only the questions nothing above could. It does not choose jobs or read your
resume file. A cover-letter field with no `questions.cover_letter` is just another open
question to it.

- **What it is given:**
  - the question;
  - the job description;
  - the options of a dropdown or radio button;
  - a candidate profile built from your config: name, location, total years of experience,
    most recent employer, `questions.headline`, desired salary, notice period, work
    authorization and visa, plus `questions.summary` and `questions.about`.
- **What it never sees:** your phone, email, street, zip, citizenship status, current salary
  or protected characteristics.
- **Never asked at all:** sensitive questions (visas, work authorization, citizenship,
  salary, disability, veteran status, clearances) and the email question. Those are answered
  from your configuration or not at all.
- **Free text:** one sentence, or at most 350 characters for an open question.
- **Dropdowns and radio buttons:** the answer counts only when it names one of the options
  exactly; anything else is dropped. It never ticks a checkbox: only an answer you approved
  does.
- **Every AI answer is remembered as pending and used right away.** On a live run the
  application is sent with it before you have reviewed it. Unlike skill years, it may also
  answer "No" or misjudge your experience.

  Review the queue with `ajai answers review` or the TUI's Answers tab. Approving or
  correcting an answer fixes every later form that asks it. The safe way to try the AI is a
  dry run first: nothing is sent, and the queue shows what it would have answered.

### Turning it on

The settings live in `config.yaml` (`ajai doctor` prints its path) and the key in the `.env`
next to it:

```bash
ajai config set ai.enabled true
ajai config set ai.provider openai        # openai, deepseek, gemini, ollama, lmstudio, vllm, custom
ajai config set ai.model gpt-5.6-sol
# AJAI_AI_API_KEY=sk-... in the .env (the TUI's Config tab writes it for you)
```

`ai.base_url` is blank for the provider's own endpoint. A key is only ever sent over HTTPS
or to localhost. `ajai doctor` shows the provider, the model and whether the key is set; it
does not call the model, so the first real call is your next run.

### Your profile

- **`questions.summary`:** a short professional summary. It is pasted as it is into
  "Summary", "Resumo" and "Sobre você" fields, and the AI reads it.
- **`questions.about`:** free text only the AI reads: your jobs with dates and stack,
  skills, education, languages, what you are looking for. Keep secrets, phone numbers and
  links out of it. Anything in it can surface in an answer.
- **`questions.headline`:** a one-line headline, for "headline" fields and the AI.

The fuller these are, the less generic the AI's answers.

### Skill years

`questions.skill_years` lists your years with each technology or role, as "name: years" (a
whole number, 1 or more):

```bash
ajai config set questions.skill_years ".net: 4, c#: 4, python: 1, aws: 1, computação em nuvem: 1"
```

They answer, as configured answers:

- **"How long have you used X?", "Há quanto tempo você usa X?", "Quanto tempo de experiência
  com X?"** get X's years; with several technologies named, the fewest.
- **"Do you have experience with X?", "Possui experiência (conhecimento, domínio) com X?"**
  get yes, never no.
  - With a number of years in the question ("at least 5 years", "no mínimo 3 anos"), yes
    only when your years reach it.
  - Options that are ranges of years are left alone.

A name matches whole, whatever its case and accents ("java" is not "JavaScript", "react" is
not "React-Native"), and only in the question itself, not its context or links. List
Portuguese names and roles too ("computação em nuvem", "dev fullstack"). A technology you
did not list, notice periods, totals and sensitive questions go on as before. Keep the
numbers true to your resume: recruiters read both.

### The help pause

With you at the terminal, a question nothing can answer pauses the job: ajai shows it (and
sends a Telegram message), you fill it in Chrome and press Enter, and what you typed is
remembered as approved, so the next form that asks goes through. The pause waits up to 15
minutes. With `--unattended`, or when stdin or stdout is not a terminal (a script, a
scheduler), the job is skipped instead and the question waits for your answer in
`ajai answers review`.

## Non-stop runs and filter pauses

With `settings.run_non_stop: true`, a run that visited every search term (or hit the
per-run cap) is followed, ten minutes later, by another, until LinkedIn's daily limit, the
hourly cap, `--max`, an error or Ctrl-C. The filters change between runs the way the
Python bot changed them: `settings.cycle_date_posted` sets the date filter to "Past 24
hours" for every later run, and `settings.alternate_sort_by` flips the sort order before
each one (a blank sort becomes "Most relevant", then "Most recent", and so on). Each run is
recorded and reported on its own; `ajai status` shows when the next one starts.

With `search.pause_after_filters: true` (the default) and you at the terminal, ajai stops
after applying each term's filters so you can check or adjust them in Chrome; press Enter
to go on, or type `off` to stop pausing for the session.

## Schedule windows

A schedule window is a span of local time on chosen weekdays, optionally with its own market
and dry run:

```bash
ajai schedule add --days mon-fri --start 09:00 --end 11:00 --market brasil
ajai schedule add --days sat --start 10:00 --end 12:00 --market internacional --dry-run
ajai schedule list            # every window, and the next opening
ajai schedule disable 2       # enable, rm work the same way
```

When a window opens, one unattended run starts (a non-stop session with
`settings.run_non_stop`); when it closes, the run is stopped. A run that ends early is not
restarted in the same window, and a window that opens while another run is in progress is
skipped. Windows never cross midnight: split one in two (`22:00`-`24:00` and `00:00`-`02:00`
the next day). `ajai status` shows the next opening.

### The daemon

Windows are acted on by `ajai daemon`, which runs until Ctrl-C:

```bash
ajai daemon install       # a systemd user unit that starts with your desktop session
ajai daemon status        # installed, running?
ajai logs --daemon -f     # what it decides, and the runs it starts
ajai daemon uninstall     # stop it and remove the unit
```

`ajai daemon install` writes `~/.config/systemd/user/ajai.service` for the binary you ran it
with (carrying your `AJAI_HOME`, XDG directories and `--config`, if you use them), enables it
for your desktop session, starts it and checks it stays up; run it again after moving or
upgrading ajai. It warns when your session does not reach `graphical-session.target` or
systemd does not know your display, either of which would keep Chrome from opening, and
refuses while an `ajai daemon` you started by hand is running. Stopping the unit is Ctrl-C:
the run in progress ends cleanly and its window resumes when the daemon is back. On your own
Chrome profile (`settings.chrome_profile_dir`), the window a daemon run opened closes with
the unit. You can also just run `ajai daemon` in a terminal you leave open.

Every 30 seconds it looks at the windows: when one opens it starts an unattended run with
that window's market and dry run (a non-stop session if `settings.run_non_stop` is on) and
tells you on Telegram; when the window closes, or you disable or remove it, the run ends
cleanly with that reason. A run that ends early is not restarted in the same window. A
window that opens while another run is in progress (one you started in a terminal, say) is
skipped, and Telegram says so; two windows back to back hand over instead. The
configuration is read afresh for every run, so `ajai config set` needs no restart. Only one
daemon runs at a time, and Ctrl-C lets the run in progress end cleanly first. Its runs use
the ajai profile, so sign in once with `ajai login` before the first window.

## Remote commands (Telegram)

With `AJAI_TELEGRAM_BOT_TOKEN` and `AJAI_TELEGRAM_CHAT_ID` set (the same bot that sends the run
summaries; the chat id is the numeric one), `ajai daemon` also takes commands from that chat:

| Command | What it does |
|---|---|
| `/status` | The run in progress (who started it, market, counts, term) or the last run, pending answers, the next window |
| `/stop` | Stop the run in progress, wherever it was started; a second message says when it has ended |
| `/run [brasil\|internacional] [dry]` | Start an unattended run now, or say why not (a run in progress, a config error, the Chrome profile in use) |
| `/pending` | Review the answer memory's pending answers from your phone (below) |
| `/help` | The list |

`/pending` sends one message per pending answer, the 10 newest at a time: the question, its
kind, the options it offered (select and radio), the current answer and who gave it, and the
job it came from. **Approve** keeps the answer shown (if a run changed it meanwhile, the
message is updated for you to check again), **Delete** forgets it; a question with no answer
yet has only Delete. To give or correct an answer, reply to its message with the text: for a
select or radio it must be one of the options, or the beginning of just one of them (case and
spacing do not matter; the option's own text is saved), otherwise the reply is refused with
the options listed. A checkbox takes only the buttons: approving it ticks it from now on. Each
button or reply edits the message to show what was done; replies keep working for 30 days,
also after the daemon restarts. Questions remembered before this version have no options
saved, so any reply is taken for them.

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
Override per run with `ajai run --market brasil`.

## Telegram

With both secrets set, ajai sends one message when a run ends (the summary, or the error that
stopped it with what was done until then), one when a run cannot start (configuration,
Chrome, AI settings; not a mistyped flag), and a nudge when it waits for your login or your
help. A failed send never affects the run, and the token never
reaches the log.

To get the two values: create a bot with [@BotFather](https://t.me/BotFather) (it gives the
token), send your bot any message, then open
`https://api.telegram.org/bot<token>/getUpdates` and copy `"chat":{"id": ...}`.

## Files

Config lives in the OS config directory (`~/.config/ajai/config.yaml` on Linux), with the
`.env` beside it. The SQLite database (history and answer memory, pure-Go driver), the logs,
the screenshots of failed applications and the resumes live in the OS data directory
(`~/.local/share/ajai` on Linux). `AJAI_HOME=<dir>` puts everything under one directory;
`--config <file>` relocates the config file alone; `AJAI_CFG_SECTION_KEY=value` overrides one
key for one process without touching the file.

## Releasing

Releases are built by GitHub Actions from a tag:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

`.goreleaser.yaml` builds the two Linux binaries (amd64, arm64) with the tag as the version (`ajai version`), and
publishes them with `checksums.txt`. Every pull request already builds them without
publishing, so a broken build shows up before a tag does.

## Honest note on detection

There is no mature anti-detection browser library in Go. `ajai` relies instead on driving
your genuine Chrome (so every network request is Chrome's own) and on conservative pacing:
15 applications an hour and 50 a run by default, with jittered pauses between jobs and
between form actions. The residual risk and the reasoning are in
`docs/adr/0001-go-rod-on-the-users-chrome.md`. Use it as a person would: a few dozen
applications a day, not hundreds.

## License

MIT. See `LICENSE` and `NOTICE`.
