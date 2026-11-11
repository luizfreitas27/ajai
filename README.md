<p align="center">
  <img src="docs/assets/logo.svg" width="380" alt="ajai">
</p>

<p align="center">
  <b>Easy Apply AI.</b><br>
  One binary. Your machine, your browser, your account.
</p>

<p align="center">
  <a href="https://github.com/luizfreitas27/ajai/releases/tag/v0.1.0"><img src="https://img.shields.io/badge/release-v0.1.0-EA580C?style=flat-square" alt="release v0.1.0"></a>
  <a href="https://github.com/luizfreitas27/ajai/actions/workflows/ci.yml"><img src="https://github.com/luizfreitas27/ajai/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.26">
  <img src="https://img.shields.io/badge/platform-Linux-FCC624?style=flat-square&logo=linux&logoColor=black" alt="Linux">
  <img src="https://img.shields.io/badge/license-MIT-16A34A?style=flat-square" alt="MIT">
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#i-want-to">I want to…</a> ·
  <a href="#the-tui">The TUI</a> ·
  <a href="#command-reference">Commands</a> ·
  <a href="#troubleshooting">Troubleshooting</a>
</p>

<p align="center">
  <img src="docs/assets/dashboard.svg" width="820" alt="The ajai Dashboard: the run in progress, the last runs and today's log">
</p>

`ajai` searches LinkedIn for jobs that match your criteria and submits Easy Apply
applications for you. It runs on your own machine, in a real, visible Chrome, with your own
account. It fills each form from your configuration, remembers every answer so you can
review it, and tells you on Telegram how a run went.

**This manual, in order:**

1. [Quick start](#quick-start): from nothing to a first test run, in about ten minutes.
2. [How it works](#how-it-works): the five ideas that make everything else obvious.
3. [I want to…](#i-want-to): the everyday tasks, each with its key and its command.
4. Guides: [the TUI](#the-tui), [answers](#answers-and-the-review-queue),
   [the AI](#the-ai-optional), [recruiter messages](#recruiter-messages-optional),
   [markets](#markets), [schedule](#running-on-a-schedule), [Telegram](#telegram).
5. Reference: [commands](#command-reference), [files](#files),
   [install details](#install-details), [troubleshooting](#troubleshooting).

## Quick start

### Before you start

| You need | Details |
|---|---|
| **Linux with a desktop** | amd64 or arm64, Wayland or X11. Chrome opens a visible window; ajai never runs headless. |
| **Chrome or Chromium** | `ajai doctor` finds it. |
| **A LinkedIn account in English** | Set LinkedIn's display language to **English** (Settings → Account preferences → Language). ajai reads the page in English. |
| **Your resume as a PDF** | |

Optional, for later: a Telegram bot (notices and remote commands) and an AI provider key.

### 1. Install

```bash
# Download the release for your machine (amd64 shown; use linux_arm64 on ARM) and its checksums
curl -fsSLO https://github.com/luizfreitas27/ajai/releases/download/v0.1.0/ajai_0.1.0_linux_amd64.tar.gz
curl -fsSLO https://github.com/luizfreitas27/ajai/releases/download/v0.1.0/checksums.txt

# Check it, unpack it, put it on your PATH
sha256sum -c --ignore-missing checksums.txt         # must say: OK
tar -xzf ajai_0.1.0_linux_amd64.tar.gz
install -m 0755 ajai ~/.local/bin/ajai

ajai version                                        # ajai v0.1.0 (…) linux/amd64
```

If `ajai` is not found, add `~/.local/bin` to your `PATH` (for example
`export PATH="$HOME/.local/bin:$PATH"` in `~/.bashrc`). Other ways to install are under
[Install details](#install-details).

### 2. Open ajai and follow the checklist

```bash
ajai
```

With nothing set up yet, ajai opens on the **setup checklist**: every thing a run needs,
with a mark for what is missing. Move with ↑↓ and press **Enter** on a line to fix it:

| Line | What Enter does |
|---|---|
| No config file | Opens a short wizard: your name, phone, city, search terms, location, salary. |
| No LinkedIn session | Opens Chrome on LinkedIn. Sign in by hand; it closes when your feed appears. |
| Resume, AI key, Telegram | Takes you to the Config tab, to set that value. |

ajai never asks for, types or stores your LinkedIn password. You sign in once, in a Chrome
profile that belongs to ajai, so your everyday Chrome stays free while it works.

### 3. Put your resume in place

Copy your PDF into the resumes directory as `resume.pdf`:

```bash
cp ~/Documents/my-cv.pdf ~/.local/share/ajai/resumes/resume.pdf
```

(`ajai doctor` prints the exact directory if yours differs.)

### 4. Tell ajai about yourself

This is what turns "skipped: unanswered questions" into applications. In the TUI's
**Config** tab (key `5`), or on the command line:

```bash
ajai config set questions.skill_years ".net: 4, c#: 4, python: 1, aws: 1"
ajai config set questions.summary "Backend developer with 5 years of experience in …"
```

See [Skill years](#skill-years) and [Your profile](#your-profile).

### 5. Do a dry run

```bash
ajai run --dry-run --fast
```

ajai searches, screens each job, fills every form and **discards it at the Review step**.
Nothing is submitted. Watch the Chrome window, then look at what it answered:

```bash
ajai answers review
```

Approve what is right, correct what is not. Each correction is used on every later form.

### 6. Apply for real

Start small, with the Chrome window visible:

```bash
ajai run --max 2      # two applications, then stop
ajai run              # a full run: 15 an hour and 50 a run, by default
```

From here on you can do everything from the TUI: type `ajai`, press `r` to start a run and
`x` to stop it.

<details>
<summary><b>Setting up by hand, without the TUI</b></summary>

```bash
ajai init                  # the wizard (or: ajai init --first-name … --terms "go,backend" …)
ajai login                 # sign in to LinkedIn once
ajai doctor                # checks everything a run needs, and prints every path
ajai run --dry-run --fast
```

Coming from the Python bot? `ajai import ~/path/to/Auto_job_applier_linkedIn` brings over
its settings, answer memory, job history and resume (never its secrets).

</details>

## How it works

Five ideas explain everything ajai does.

**1. A run.** One pass over your search terms. For each term ajai applies LinkedIn's
filters, then goes job by job:

```
search → screen the job → open Easy Apply → answer each question → submit → record it
```

A job is *screened out* by your rules (blacklisted companies and words, required experience,
security clearance, sponsorship) or because ajai already handled it. Only one run happens on
your machine at a time. A **dry run** does everything except submit.

**2. Answers never come from a guess.** Each question on a form is answered by the first of
these that can:

1. your **configuration** (name, phone, city, visa, salary, notice period, years of experience, skill years);
2. the **answer memory** (what you approved or typed before);
3. the **AI**, only if you turned it on;
4. **nobody**: the job is skipped and the question waits for you.

**3. The review queue.** Questions nobody could answer, and answers the AI gave, wait in a
queue. You answer or approve each one once, and every later form that asks it goes through.
The first runs skip many jobs; a few minutes in the queue fixes that. This is the part of
ajai that gets better as you use it.

**4. Pacing.** 15 applications an hour and 50 a run by default, with jittered pauses and
typing one character at a time. ajai is meant to behave like a person, not to be fast.

**5. Attended or unattended.** With you at the terminal, ajai may pause and ask for help.
Started from the TUI, the schedule or Telegram, it never pauses: it skips and queues.

| Word | Meaning |
|---|---|
| **Run** | One pass over your search terms, ending in a summary. |
| **Dry run** | A run that fills forms and discards them. Nothing is sent. |
| **Answer memory** | Every answer given, pending or approved. |
| **Review queue** | The pending part of the answer memory: what waits for you. |
| **Market** | `brasil` or `internacional`: currency, salary period, terms and resume. |
| **Schedule window** | Weekdays and hours in which runs start and stop by themselves. |
| **Daemon** | The background process that acts on the windows and on Telegram commands. |
| **ajai profile** | The Chrome profile ajai owns, where your LinkedIn session lives. |

## I want to…

| I want to… | In the TUI (`ajai`) | On the command line |
|---|---|---|
| Start a run | `r` | `ajai run` |
| Try without submitting | `r`, dry run (the default) | `ajai run --dry-run` |
| Stop the run | `x` | `ajai stop`, or Ctrl-C in its terminal |
| See what is happening | `1` Dashboard | `ajai status`, `ajai logs -f` |
| Answer the questions it could not | `2` Answers | `ajai answers review` |
| See where I applied | `4` Jobs | `ajai jobs list --applied` |
| See why a job failed or was skipped | `4` Jobs, ←→ to filter | `ajai jobs list --failed` / `--skipped` |
| Change a setting | `5` Config | `ajai config set <key> <value>` |
| Search other terms, just this once | | `ajai run --terms "go,backend" --location "Remote"` |
| Apply in the other market, just this once | `r`, pick the market | `ajai run --market brasil` |
| Run every weekday morning | `3` Schedule, `n`, then `i` | [Running on a schedule](#running-on-a-schedule) |
| Control it from my phone | | [Telegram](#telegram) |
| Check that everything is set up | `s` | `ajai doctor` |
| Export my applications to a spreadsheet | | `ajai export --csv ./sheets` |

## The TUI

Type `ajai` with no command, in a terminal, and it opens a full-screen interface. Outside a
terminal (a pipe, a script, systemd) it prints the help instead.

| Tab | What it is for |
|---|---|
| **1 Dashboard** | The run in progress, the last runs, pending answers, the next window, the daemon, today's log |
| **2 Answers** | The answer memory: review, approve, correct or delete what was answered |
| **3 Schedule** | Schedule windows and the daemon that acts on them |
| **4 Jobs** | The job history, filtered and searchable; open a job in your browser |
| **5 Config** | Every setting, with what it does, edited one at a time; the secrets in hidden fields |
| **s Setup** | Every check `ajai doctor` makes, with what fixes it |

### Keys

They mean the same on every screen. The footer shows the keys of the screen you are on, and
`?` lists them in full.

| Key | What it does |
|---|---|
| `1`–`5`, Tab, Shift-Tab | Switch tabs |
| ↑↓ (`k` `j`), PgUp PgDn, `g` `G` | Move in a list |
| ←→ (`h` `l`) | Change the list's filter |
| Enter | The selected item's main action: edit it, or open the job |
| `r` / `x` | Start a run / stop the run in progress, from any tab |
| `s` | The setup checklist |
| `n` `d` `a` Space `/` | New, delete, approve, turn on or off, search |
| `?` | Help for this screen |
| `q`, Ctrl-C | Quit. **Quitting never stops a run.** |

<table>
  <tr>
    <td><img src="docs/assets/jobs.svg" alt="The Jobs tab: the job history with the selected job's details"></td>
  </tr>
  <tr>
    <td><img src="docs/assets/answers.svg" alt="The Answers tab: the review queue with the selected answer's details"></td>
  </tr>
</table>

The list tabs share one layout: a header with the title, the filters (the one shown in
brackets) and a summary; the list, with a `▌` bar on the selected row; and the selected
item's details, to the right of the list on a terminal 120 columns wide or more, under it on
a narrower one. Flame orange marks what is active; green, amber and red mean ok, warning and
failure. The TUI needs a terminal of at least 80×24 and honours `--no-color` and `NO_COLOR`.

<details>
<summary><b>1 Dashboard: starting and stopping runs</b></summary>

The Dashboard refreshes every second. It shows:

- the run in progress: who started it (the terminal, a schedule window, Telegram, the TUI),
  the market, dry run or live, the term being searched and the counts so far;
- the last runs and why each ended;
- the pending answers and the next schedule window;
- whether the daemon runs, and whether every setup check passes;
- today's log as it grows.

**`r` starts a run**, from any tab. You choose the market, dry run (the default) or live, and
a maximum of applications. Live shows in red and asks once more, since it submits
applications for real. The run is a process of its own (`ajai run --unattended`): closing the
TUI never ends it, and questions nothing can answer go to the review queue. If a run is
already in progress, ajai says which one and starts nothing; if the new run cannot start (a
config problem, Chrome missing or in use), it says why.

**`x` stops the run in progress**, whoever started it, after asking. It ends once the job in
hand is done, recorded as "stopped from the TUI". When a run ends while the TUI is open, a
line says how it went.

</details>

<details>
<summary><b>2 Answers: the review queue</b></summary>

The review queue comes first (the tab title counts the pending answers); the approved ones
are a ←→ away. Each answer shows its question, kind, options, who gave it, how often it was
used and the last job.

- `a` approves the answer as shown. If a run answered it again meanwhile, nothing is
  approved and the new answer is shown.
- Enter corrects it. A select or radio is picked from its options with ↑↓, or typed (an
  option, or the beginning of just one). Anything else is typed. A correction is yours and
  counts as approved.
- `d` deletes it after asking; ajai asks the question again next time.
- A checkbox takes only `a` or `d`.

</details>

<details>
<summary><b>3 Schedule: windows and the daemon</b></summary>

The schedule windows (weekdays, hours, market, mode, on or off) with each one's next opening.

- `n` adds one in a form: tick the weekdays with Space, type the start and end (HH:MM, local
  time), choose the market and dry run or live. A live window asks once more: its runs submit
  applications. A window that crosses midnight or overlaps another on the same weekday is
  refused, with the reason.
- Space turns the selected window off or back on; `d` deletes it after asking.
- The selected window's details show its next three openings and what its last opening did:
  the run it started, with its counts and why it ended, or why no run started.

Windows open runs only while the daemon runs. A strip under the tab's title says whether it
does. `i` installs (or reinstalls) its systemd unit, `u` uninstalls it, and `t` starts or
stops the daemon, each after a question that says what it does.

</details>

<details>
<summary><b>4 Jobs: the history</b></summary>

The job history, newest first, with the counts per outcome. ←→ show every job or only the
applied, external, failed or skipped ones. `/` searches titles, companies and locations,
ignoring case and accents (Esc clears the search). The selected job shows its outcome and
why, when it was recorded and posted, a failure's error and screenshot, its links and the
questions answered. Enter opens it in your default browser: the company's application page
for an external job, LinkedIn's otherwise.

</details>

<details>
<summary><b>5 Config: settings and secrets</b></summary>

Every setting by section, with its value in the config file and what it does. This is the
place to learn what can be configured. Enter edits one:

- a setting with fixed values is picked from a list (several, with Space, for a list such as
  `search.on_site`);
- anything else is typed, and refused with the reason if it is not valid;
- a setting your environment or `.env` overrides (`AJAI_CFG_...`) is marked; an edit changes
  the file, never the override.

The three secrets (`AJAI_AI_API_KEY`, `AJAI_TELEGRAM_BOT_TOKEN`, `AJAI_TELEGRAM_CHAT_ID`) show
only whether they are set, and where. Each one is typed into a hidden field and saved in
ajai's `.env`, readable only by you; an empty value removes it. If the daemon runs, the TUI
tells you when it needs a restart: it reads the Telegram settings when it starts, while
other settings take effect on its next run.

</details>

<details>
<summary><b>s Setup: the checklist</b></summary>

Every check `ajai doctor` makes, with what fixes it. The TUI opens on it when something a run
needs is missing; `s` opens it at any time. The checks run again every half minute and after
every change. Enter fixes the selected check:

- no config file: the init wizard (`ajai init`);
- no LinkedIn session in the ajai profile: `ajai login`, which opens Chrome to sign in;
- the AI key, Telegram or the resume: the Config tab.

`i` imports from the Python bot after asking for its folder, and `r` checks again. The
wizard, the sign-in and the import run in the same terminal while the TUI steps aside; press
Enter at the end to come back. Without a config file only the checklist and the Config tab
(for the secrets) open.

</details>

## Answers and the review queue

Every question in an Easy Apply form is answered by the first of these that can (English
and Portuguese labels alike):

1. **Your configuration.** Name, phone, city, work authorization, visa, salary, notice period
   and total years of experience. Also your [skill years](#skill-years), for questions about
   one technology.
2. **The answer memory.** What you approved or typed before, and what the AI answered before.
3. **The AI**, only with `ai.enabled: true`.
4. **Nothing.** The job is skipped and the question goes to the review queue, or, with you
   at the terminal, to a help pause (below).

Review the queue in the TUI's Answers tab, with `ajai answers review`, or from Telegram with
`/pending`.

### Skill years

`questions.skill_years` lists your years with each technology or role, as "name: years" (a
whole number, 1 or more):

```bash
ajai config set questions.skill_years ".net: 4, c#: 4, python: 1, aws: 1, computação em nuvem: 1"
```

They answer:

- **"How long have you used X?", "Há quanto tempo você usa X?", "Quanto tempo de experiência
  com X?"** with X's years; with several technologies named, the fewest.
- **"Do you have experience with X?", "Possui experiência (conhecimento, domínio) com X?"**
  with yes, never no.
  - With a number of years in the question ("at least 5 years", "no mínimo 3 anos"), yes
    only when your years reach it.
  - Options that are ranges of years are left alone.

A name matches whole, whatever its case and accents ("java" is not "JavaScript", "react" is
not "React-Native"), and only in the question itself. List Portuguese names and roles too
("computação em nuvem", "dev fullstack"). Keep the numbers true to your resume: recruiters
read both.

### Your profile

- **`questions.summary`:** a short professional summary. It is pasted as it is into
  "Summary", "Resumo" and "Sobre você" fields, and the AI reads it.
- **`questions.headline`:** a one-line headline, for "headline" fields and the AI.
- **`questions.about`:** free text only the AI reads: your jobs with dates and stack,
  skills, education, languages, what you are looking for. Keep secrets, phone numbers and
  links out of it: anything in it can surface in an answer. You can also steer how the AI
  answers here (for example, to count related experience), as guidance, not a rule.

The fuller these are, the less generic the AI's answers.

### The help pause

With you at the terminal (`ajai run`, not a run started from the TUI), a question nothing can
answer pauses the job. ajai shows it (and sends a Telegram message), you fill it in Chrome
and press Enter, and what you typed is remembered as approved. The pause waits up to 15
minutes.

With `--unattended`, or when stdin or stdout is not a terminal (a script, the schedule, a run
started from the TUI), the job is skipped instead and the question waits in the review queue.

## The AI (optional)

The AI answers only the questions that your configuration and the answer memory could not.
It does not choose jobs and does not read your resume file.

### Turning it on

```bash
ajai config set ai.enabled true
ajai config set ai.provider gemini        # openai, deepseek, gemini, ollama, lmstudio, vllm, custom
ajai config set ai.model gemini-flash-lite-latest
```

Then set the key, `AJAI_AI_API_KEY`, in the TUI's Config tab (a hidden field), or in the
`.env` file next to the config file.

| Provider | Key | Notes |
|---|---|---|
| `gemini` | [Google AI Studio](https://aistudio.google.com/) → Get API key | Has a free tier for the Flash and Flash-Lite models, with daily limits. |
| `openai` | [platform.openai.com](https://platform.openai.com/) → API keys | Prepaid API credits. A ChatGPT plan does **not** include them. |
| `deepseek` | [platform.deepseek.com](https://platform.deepseek.com/) | Paid, low cost per token. |
| `ollama`, `lmstudio`, `vllm` | none | A model on your own machine; `ai.base_url` if not the default port. |

`ai.base_url` is blank for the provider's own endpoint. A key is only ever sent over HTTPS
or to localhost. `ajai doctor` shows the provider, the model and whether the key is set; it
does not call the model, so the first real call is your next run.

**Try it on a dry run first.** Nothing is sent, and the review queue shows what the AI would
have answered.

### What the AI does, and what it never does

- **What it is given:** the question; the job description; the options of a dropdown or
  radio button; and a candidate profile built from your config (name, location, total years
  of experience, most recent employer, `questions.headline`, desired salary, notice period,
  work authorization and visa, `questions.summary` and `questions.about`).
- **What it never sees:** your phone, email, street, zip, citizenship status, current salary
  or protected characteristics.
- **Never asked at all:** sensitive questions (visas, work authorization, citizenship,
  salary, disability, veteran status, clearances) and the email question. Those are answered
  from your configuration or not at all.
- **Free text:** one sentence, or at most 350 characters for an open question. A cover-letter
  field with no `questions.cover_letter` is an open question like any other.
- **Dropdowns and radio buttons:** the answer counts only when it names one of the options
  exactly. It never ticks a checkbox: only an answer you approved does.
- **Every AI answer is remembered as pending and used right away.** On a live run the
  application is sent with it before you have reviewed it, and the AI may answer "No" or
  misjudge your experience. Review the queue: approving or correcting an answer fixes every
  later form that asks it.

## Recruiter messages (optional)

> Not in release v0.1.0 yet: until the next release, [build from source](#build-from-source).

After a submitted application, ajai can write a short note to one of the job's hirers. It is
off by default:

```bash
ajai config set messages.enabled true
```

**How it goes, by default:**

1. You apply. If the job shows a hirer under "Meet the hiring team" with a free **Message**
   button, ajai queues a message for them.
2. You read the queue and approve what may go out:

   ```bash
   ajai messages list                          # every message, its text and a digest of it
   ajai messages approve <id> <digest>         # one message, if its text still has that digest
   ajai messages approve --all <list-digest>   # the whole queue, if it is still the one listed
   ajai messages delete <id>                   # remove one
   ```

   The digest is how ajai knows you approve the exact text you read. `list` prints the
   ready-made `approve` lines under the queued messages.
3. Approved messages go out at the end of the next live run, never on a dry run.

**The limits, always:**

- **Free messages only.** With only Premium or a paid InMail on offer, nothing is sent and
  the job is recorded as `no_recipient` (`ajai messages list --state no_recipient`).
- **One message per recruiter, ever.** A staffing recruiter can be the hirer on many jobs,
  and several messages in a row look like spam. A later application to one of their jobs
  records "already messaged this recruiter".
- **A daily cap:** `messages.max_per_day` (3 by default) per local day, each after a pause of
  `messages.between_min_seconds` to `messages.between_max_seconds` (90 to 300 by default). An
  approved message a run does not reach waits for the next one.
- **Never sent twice.** If ajai stops in the middle of a send, that message is marked
  `failed` at the next run and is not retried.

### The text

ajai picks a template by the language of the job posting: `messages.templates.en`, `.pt` or
`.es` in `config.yaml`. When it cannot tell the language (a short or mixed text), or that
template is blank, it uses the active market's `message_template`. A blank market template
and an unsure language queue nothing.

Every template can use three placeholders:

- `{first_name}`: the hirer's first name, capitalised. With no name, the greeting goes
  without it.
- `{title}`: the job's title.
- `{company}`: the company.

### Sending without a run

```bash
ajai messages send                                        # send what is approved, now
ajai messages send --applied-since 2026-10-20 --dry-run   # also: what would past applications get?
ajai messages send --applied-since 2026-10-20             # send those too
```

`ajai messages send` opens Chrome as a run does and sends the approved messages with the
same cap, pauses and checks. It never searches or applies, so it works after LinkedIn's daily
application limit. With `--applied-since`, it also goes back over the jobs applied to on or
after that date that have no hirer recorded, newest first: a free **Message** button to a
recruiter not messaged before gets a message, sent right away. Each job gets one line: sent,
would send, no free message, already messaged, cap reached or failed.

### Skipping the approval

```bash
ajai config set messages.auto_send true
```

Each message is then sent right after its application, before the next job is opened. The
daily cap, the free-only checks and the one-per-recruiter rule still hold; a message past the
cap stays approved and goes out at the end of a later run. A block from LinkedIn stops the
sends for the rest of the run, not the applications.

**The risk:** every message goes out unseen, with your template's text filled in. Read your
templates before you turn it on.

> Messaging carries more risk than applying. LinkedIn watches messages more closely than
> applications, and a restriction on the account would affect the applications too. Keep
> `max_per_day` low.

## Markets

`markets.active` is `internacional` (foreign currency, annual salaries) or `brasil` (BRL,
monthly salaries). Each market may set its own currency, salaries, search location, search
terms and resume (`ajai config set markets.brasil.resume cv-pt.pdf`); blank values fall back
to the general settings.

Salary questions get the figure in the period they ask for, and the market's own period
when they name none. An hourly-rate question ("valor hora", "hourly rate") gets the monthly
figure divided by the monthly hours it names, or by 168.

Override the market for one run with `ajai run --market brasil`.

## Running on a schedule

A **schedule window** is a span of local time on chosen weekdays, optionally with its own
market and dry run. When it opens, a run starts; when it closes, the run is stopped.

```bash
# 1. Add windows
ajai schedule add --days mon-fri --start 09:00 --end 11:00 --market brasil
ajai schedule add --days sat --start 10:00 --end 12:00 --market internacional --dry-run

# 2. Install the daemon that acts on them (a systemd user unit, started with your desktop session)
ajai daemon install

# 3. Look
ajai schedule list        # every window, and the next opening
ajai daemon status        # installed, running?
ajai logs --daemon -f     # what it decides, and the runs it starts
```

`ajai schedule disable 2`, `enable 2` and `rm 2` change a window; `ajai daemon uninstall`
stops the daemon and removes its unit. The TUI's Schedule tab does all of this too.

Good to know:

- Sign in once with `ajai login` before the first window: nobody is there to sign in for a
  scheduled run.
- Scheduled runs are unattended: they never pause, they skip and queue.
- Windows never cross midnight: split one in two (`22:00`–`24:00` and `00:00`–`02:00` the
  next day).
- A run that ends early is not restarted in the same window. A window that opens while
  another run is in progress is skipped, and Telegram says so.
- The configuration is read afresh for every run, so `ajai config set` needs no restart.
- After moving or upgrading ajai, run `ajai daemon install` again.

<details>
<summary><b>How the daemon and its unit behave</b></summary>

`ajai daemon install` writes `~/.config/systemd/user/ajai.service` for the binary you ran it
with (carrying your `AJAI_HOME`, XDG directories and `--config`, if you use them), enables it
for your desktop session, starts it and checks it stays up. It warns when your session does
not reach `graphical-session.target` or systemd does not know your display, either of which
would keep Chrome from opening, and refuses while an `ajai daemon` you started by hand is
running. Stopping the unit is like Ctrl-C: the run in progress ends cleanly and its window
resumes when the daemon is back. You can also just run `ajai daemon` in a terminal you leave
open.

Every 30 seconds the daemon looks at the windows: when one opens it starts an unattended run
with that window's market and dry run and tells you on Telegram; when the window closes, or
you disable or remove it, the run ends cleanly with that reason. Two windows back to back
hand over. Only one daemon runs at a time.

</details>

### Non-stop runs

With `settings.run_non_stop: true`, a run that visited every search term (or hit the per-run
cap) is followed, ten minutes later, by another, until LinkedIn's daily limit, the hourly
cap, `--max`, an error or Ctrl-C. The filters can change between runs:
`settings.cycle_date_posted` sets the date filter to "Past 24 hours" for every later run,
and `settings.alternate_sort_by` flips the sort order before each one. Each run is recorded
and reported on its own; `ajai status` shows when the next one starts.

### Filter pauses

With `search.pause_after_filters: true` (the default) and you at the terminal, ajai stops
after applying each term's filters so you can check or adjust them in Chrome. Press Enter to
go on, or type `off` to stop pausing for the session.

## Telegram

With a Telegram bot, ajai sends you one message when a run ends (the summary, or the error
that stopped it), one when a run cannot start, and a nudge when it waits for your login or
your help.

**Setting it up:**

1. Create a bot with [@BotFather](https://t.me/BotFather). It gives you the token.
2. Send your bot any message.
3. Open `https://api.telegram.org/bot<token>/getUpdates` and copy the number in
   `"chat":{"id": ...}`.
4. Save both in the TUI's Config tab (hidden fields), or in the `.env` next to the config
   file:

   ```bash
   AJAI_TELEGRAM_BOT_TOKEN=123:ABC
   AJAI_TELEGRAM_CHAT_ID=987654321
   ```

A failed send never affects the run, and the token never reaches the log.

### Remote commands

While the daemon runs (`ajai daemon install`), the same bot takes commands from that chat:

| Command | What it does |
|---|---|
| `/status` | The run in progress (who started it, market, counts, term) or the last run, pending answers, the next window |
| `/run [brasil\|internacional] [dry]` | Start an unattended run now, or say why not |
| `/stop` | Stop the run in progress, wherever it was started; a second message says when it has ended |
| `/pending` | Review the pending answers from your phone |
| `/help` | The list |

`/pending` sends one message per pending answer, the 10 newest at a time, with **Approve**
and **Delete** buttons. To give or correct an answer, reply to its message with the text.

<details>
<summary><b>The details: replies, safety and what happens after a restart</b></summary>

Each `/pending` message shows the question, its kind, the options it offered (select and
radio), the current answer and who gave it, and the job it came from. **Approve** keeps the
answer shown (if a run changed it meanwhile, the message is updated for you to check again),
**Delete** forgets it; a question with no answer yet has only Delete. A reply to a select or
radio must be one of the options, or the beginning of just one of them (case and spacing do
not matter); otherwise it is refused with the options listed. A checkbox takes only the
buttons: approving it ticks it from now on. Each button or reply edits the message to show
what was done; replies keep working for 30 days, also after the daemon restarts.

Messages from any other chat are ignored. The daemon asks Telegram for new messages (long
polling), so nothing on your machine is reachable from the internet, and it remembers what
it read, so a restart never repeats a command. A `/run` or `/stop` that waited more than 10
minutes while the daemon was down is not acted on (send it again). If your bot has a webhook
set, or another program reads it, Telegram hands the messages to that one: the daemon says so
once, on Telegram too (remove the webhook with `deleteWebhook`). A revoked token stops the
remote commands, with a line in the daemon's log. The token and chat id are read when the
daemon starts.

</details>

## Command reference

`ajai <command> --help` explains each one in full.

| Command | What it does |
|---|---|
| `ajai` | The TUI |
| **Setting up** | |
| `ajai init` | Create the config file with a short wizard (or from flags) |
| `ajai login` | Sign in to LinkedIn once, in the profile ajai uses |
| `ajai doctor` | Check everything a run needs, and print every path |
| `ajai config show` / `get` / `set` / `validate` / `path` | The configuration, secrets masked |
| `ajai import <folder>` | Bring settings, answers and history over from the Python bot |
| **Running** | |
| `ajai run [--dry-run] [--max N] [--terms "a,b"] [--location X] [--market brasil]` | One run; the flags override the config for this run only |
| `ajai run --dry-run --fast` | A dry run with short pauses (`--fast` only works with `--dry-run`) |
| `ajai run --unattended` | Never pause for help; skip and queue instead |
| `ajai run --json` | One JSON object per line for every event and summary, for scripts |
| `ajai stop` | Stop the run in progress cleanly, from any terminal |
| `ajai status` | The run in progress (live counters, who started it) and the last runs |
| `ajai logs [-f] [--daemon]` | Today's log; `-f` follows it; `--daemon` shows the daemon's |
| **Reviewing** | |
| `ajai answers review` | Work through the pending answers: approve, edit, delete, skip |
| `ajai answers list [--state pending]` | The answer memory; also `approve`, `edit`, `delete`, `clear` |
| `ajai jobs list [--applied] [--failed] [--external] [--skipped] [--since 2026-10-01] [--json]` | The job history |
| `ajai export --csv ./sheets` | The job history as spreadsheets |
| `ajai messages list` / `approve` / `delete` / `send` | [Recruiter messages](#recruiter-messages-optional) |
| **Automating** | |
| `ajai schedule add` / `list` / `rm` / `enable` / `disable` | Schedule windows |
| `ajai daemon install` / `status` / `uninstall` | The daemon as a systemd user unit |
| `ajai daemon` | The daemon in this terminal, until Ctrl-C |
| **Other** | |
| `ajai version` | The version, commit and platform |
| `ajai completion bash\|zsh\|fish` | Shell completion |

Only one run happens on a machine at a time: a second `ajai run` refuses and says which run
is in progress. `ajai stop` asks that run to end: the job in progress is finished or
discarded, the run is recorded and its summary sent. A run whose process died without ending
(killed, crashed) is shown as ended, "did not exit cleanly", the next time anything looks.

Every command takes `--config <file>` and `--no-color`.

## Files

| What | Where |
|---|---|
| Configuration | `~/.config/ajai/config.yaml` |
| Secrets | `~/.config/ajai/.env`, readable only by you |
| History and answer memory (SQLite) | `~/.local/share/ajai/ajai.db` |
| Logs | `~/.local/share/ajai/logs/` |
| Screenshots of failed applications | `~/.local/share/ajai/screenshots/` |
| Resumes | `~/.local/share/ajai/resumes/` |
| The ajai Chrome profile | `~/.local/share/ajai/chrome-profile/` |

`ajai doctor` prints every path in use. To change them:

- `AJAI_HOME=<dir>` puts everything under one directory;
- `--config <file>` relocates the config file alone;
- `AJAI_CFG_SECTION_KEY=value` overrides one setting for one process, without touching the
  file (for example `AJAI_CFG_SEARCH_LOCATION=Lisboa ajai run`).

Secrets never live in `config.yaml`. Everything stays on your machine: ajai talks only to
LinkedIn (through your Chrome), to the AI provider you chose and to Telegram, if you set
them up.

## Install details

### Build from source

With Go 1.26 or newer:

```bash
go install github.com/luizfreitas27/ajai/cmd/ajai@latest
```

or

```bash
git clone https://github.com/luizfreitas27/ajai.git && cd ajai
go build ./cmd/ajai && install -m 0755 ajai ~/.local/bin/ajai
```

A build from source reports a pseudo-version (`ajai version`). Releases carry the tag. Every
release is on the [Releases](https://github.com/luizfreitas27/ajai/releases) page.

### Upgrade

Download and install the new release the same way: your configuration, answer memory,
history and Chrome profile stay where they are. If you installed the daemon's unit, run
`ajai daemon install` again so it points at the new binary.

### Uninstall

```bash
ajai daemon uninstall                 # only if you installed the daemon's unit
rm ~/.local/bin/ajai
```

That leaves your data. To remove it too (configuration and secrets, the history and answer
memory, logs, screenshots and the Chrome profile with its LinkedIn session):
`rm -r ~/.config/ajai ~/.local/share/ajai`.

### Using your everyday Chrome profile

By default ajai uses a Chrome profile of its own and closes its Chrome when a run ends. To
drive your everyday profile instead, set `settings.chrome_profile_dir` to its path (`ajai
doctor` prints it). Chrome must not be open on that profile when a run starts, and ajai
leaves the window open when the run ends.

## Troubleshooting

Start with `ajai doctor`: it checks everything a run needs and says what is wrong.

<details>
<summary><b>Almost every job is skipped for "unanswered questions"</b></summary>

This is normal on the first runs. Those questions wait in the review queue: answer them once
in the TUI's Answers tab or with `ajai answers review`, and every later form that asks them
goes through. Fill `questions.skill_years` for the "how long have you used X" questions, and
consider turning the AI on. Check that LinkedIn's display language is English.

</details>

<details>
<summary><b>"Failed to connect to Wayland display" when a run starts</b></summary>

The shell that started ajai has no `WAYLAND_DISPLAY` (or `DISPLAY`), so Chrome cannot open a
window. Start ajai from a terminal in your desktop session, or set it for the command, for
example `WAYLAND_DISPLAY=wayland-1 ajai run --dry-run` (the socket name is in
`/run/user/$UID/`).

</details>

<details>
<summary><b>A run seems frozen, with nothing new in the log</b></summary>

Keep the Chrome window visible while a run works. On Wayland a window that is hidden (on
another workspace, or under a full-screen game) stops drawing, and the mouse moves ajai makes
before each click wait for it. Bring the window back into view and the run goes on. `ajai
stop` ends a run cleanly from any terminal.

</details>

<details>
<summary><b>ajai waits for me to sign in on every run</b></summary>

The LinkedIn session is not in the profile ajai uses. Run `ajai login` once and sign in
there; `ajai doctor` then shows the session as found.

</details>

<details>
<summary><b>The AI never answers: "429 insufficient_quota" in the log</b></summary>

The provider refused the key for lack of credits. On OpenAI the API is billed apart from a
ChatGPT plan: add credits at platform.openai.com, or switch to a provider with a free tier
(`ajai config set ai.provider gemini` and a Google AI Studio key). A "503 high demand" from
Gemini is temporary; the Flash-Lite models are the least loaded.

</details>

<details>
<summary><b>"a run is in progress" although none is</b></summary>

`ajai status` says which run holds the machine and who started it. If its process is gone
(killed, crashed), the status ends it as "did not exit cleanly" and the next run starts
normally.

</details>

<details>
<summary><b>The scheduled runs never start</b></summary>

`ajai daemon status` must say the daemon is installed and running, and `ajai schedule list`
must show the window as on, with a next opening. `ajai logs --daemon` says what the daemon
decided at each opening, and why it skipped one.

</details>

## Honest note on detection

Automating a LinkedIn account goes against LinkedIn's terms of use, and the account is
yours: use ajai at your own risk.

There is no mature anti-detection browser library in Go. `ajai` relies instead on driving
your genuine Chrome (so every network request is Chrome's own) and on conservative pacing:
15 applications an hour and 50 a run by default, with jittered pauses between jobs and
between form actions, including after a resume upload and before Submit. The residual risk
and the reasoning are in `docs/adr/0001-go-rod-on-the-users-chrome.md`. Use it as a person
would: a few dozen applications a day, not hundreds.

## Development

`ajai` is the Go successor of the Python
[Auto_job_applier_linkedIn fork](https://github.com/luizfreitas27/Auto_job_applier_linkedIn).
The behaviour was ported, not reinvented: same selectors, same question heuristics, same
answer memory. See `NOTICE`.

```bash
go build ./cmd/ajai
go test ./...                 # unit tests
go test -tags browser ./...   # also the end-to-end tests, in a headless Chrome
go vet ./... && gofmt -l .
```

`CONTEXT.md` holds the project's vocabulary and `docs/adr/` its decisions. The screenshots
in `docs/assets/` are SVGs drawn from the TUI's own coloured screens.

<details>
<summary><b>Releasing</b></summary>

Releases are built by GitHub Actions from a tag:

```bash
# 1. In CHANGELOG.md, rename "Unreleased" to the new tag (for example "## v0.2.0"), and
#    update the release badge and the download links at the top of this README; merge that.
# 2. Tag the merge commit and push the tag:
git tag -a v0.2.0 -m "ajai v0.2.0" && git push origin v0.2.0
```

`.goreleaser.yaml` builds the two Linux binaries (amd64, arm64) with the tag as the version,
and publishes them with `checksums.txt` and the CHANGELOG section as the release notes. Every
pull request already builds them without publishing.

</details>

## License

MIT. See `LICENSE` and `NOTICE`.
