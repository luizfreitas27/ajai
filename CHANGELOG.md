# Changelog

Release notes, newest first. When a `v*` tag is pushed, the release workflow publishes the
section whose heading is that tag (rename "Unreleased" to it first, e.g. `## v0.2.0`); with
no such section the notes are the commit list.

## Unreleased

### Fixed

- ajai's files are now readable only by you. They used to be 0755 and 0644, so other users
  on the machine could read your job history, answer memory and resume.
  - The config and data directories, with the logs, screenshots and resumes, are 0700.
  - The database, an imported resume and exported spreadsheets are 0600.
  - An existing install is tightened the next time ajai starts.
  - A directory you chose is created private when missing, but never changed: `AJAI_HOME`
    itself, the one `--config` points into, an export folder.
- After an application, ajai closes LinkedIn's "Application sent" dialog. It used to look
  for any "Done" on the page, so a company name holding the word (Software Delivery Done
  Right) was clicked instead, the dialog stayed over the page and every card after it
  failed with "something stayed drawn over it".
  - That error now names what is drawn over the card (tag, id, class, role, aria-label,
    into LinkedIn's messaging overlay's shadow root) and saves a screenshot.
  - Two cards in a row covered end the run, instead of waiting 10 s on every card left.
- A multiple-choice question drawn with checkboxes (LinkedIn's "Yes" / "No" sponsorship
  questions) failed with "element's pointer-events is none": the boxes take no pointer events,
  and the click never reached them. ajai now picks the option through its label, then unticks
  any other option of the question that was ticked.

### Docs

- The README opens with the flame logo, badges and screenshots of the TUI, says step by step
  how to download a release (with its checksum), upgrade and uninstall, and gains a
  troubleshooting section and a table of the AI providers.

## v0.1.0

### Renamed: aja is now ajai

- **The tool is called ajai.**
  - The command is `ajai`; the module and the repository are `luizfreitas27/ajai`.
  - Every environment variable starts with `AJAI_`: `AJAI_HOME`, `AJAI_AI_API_KEY`,
    `AJAI_TELEGRAM_BOT_TOKEN`, `AJAI_TELEGRAM_CHAT_ID`, `AJAI_CFG_*`.
  - The config lives in `~/.config/ajai` and the data in `~/.local/share/ajai` (the
    database is `ajai.db`). The daemon's unit is `ajai.service`.
- **The old names are not read.** If you had aja installed, move your setup by hand once:
  1. Run `aja daemon uninstall` if you installed the unit.
  2. Move `~/.config/aja` to `~/.config/ajai` and `~/.local/share/aja` to
     `~/.local/share/ajai`. The Chrome profile moves with it and stays signed in.
  3. Rename `aja.db` there to `ajai.db`, with its `-wal` and `-shm` files if present, and
     the logs' `aja-` prefix to `ajai-` so that `ajai logs` still finds them.
  4. Rename the `AJA_` variables in `.env` to `AJAI_`.
  5. Install `ajai`, and run `ajai daemon install` again if you used the unit.

### Upgrading from phase 1

- **ajai now uses a Chrome profile of its own.** With `settings.chrome_profile_dir` blank
  (the default) runs use the ajai profile in the data directory, so your everyday Chrome
  stays open and free. Run `ajai login` once to sign in to LinkedIn there, or set
  `settings.chrome_profile_dir` to your everyday profile (`ajai doctor` prints its path)
  to keep the old behaviour. On the ajai profile a run closes its Chrome when it ends; on
  yours it leaves the window open, as before.
- **Linux only for now.** Releases are built for linux/amd64 and linux/arm64.

### Fixed

- A dropdown still on LinkedIn's Portuguese placeholder ("Selecionar opção"), common in
  employers' own questions, counts as unanswered: it is answered from the config or the
  answer memory, or the job is skipped with the question in the review queue. It used to
  pass for an answer, and the job failed after 15 pages.
- When LinkedIn refuses a page of the Easy Apply form, the job ends unanswered within two
  tries instead of pressing Next fifteen times and failing. A question LinkedIn flags
  ("Please enter a valid answer") is named with its message and queued for review, or
  pointed to the config when the config gave the answer; a page that stays with nothing
  flagged is said so. A correction typed in a help pause is remembered.
- The Easy Apply filter (and the other filter switches) is set again: LinkedIn draws its
  toggle over the switch's input, which refused ajai's click, so searches returned
  external jobs that were then skipped. The switch is clicked where it shows, as a person
  would, never through something drawn over it; one already on is left on, and one the
  click leaves off is logged.
- A button that never becomes clickable no longer hangs the run. A click, typing into a
  field or picking from a dropdown waits up to 10 seconds for the element to be shown,
  enabled and uncovered, then gives up saying why ("it stayed disabled", "it stayed
  hidden", "something stayed drawn over it"). The job fails at that step, and the run goes
  on to the next job. It used to wait forever, with nothing in the log, and `ajai stop`
  could not end it. A Next button LinkedIn redraws while ajai waits for it, as it does
  while it takes an uploaded resume, is found again and pressed.
- When LinkedIn redraws the whole Easy Apply modal while ajai waits on its Next button,
  the modal is found again and the application goes on in it. The job used to fail with
  "clicking Next: no element matches".
- A run no longer opens a job it already settled when LinkedIn lists it again, nor a
  second posting of the same role (same title, company and location). They are counted as
  skips ("already handled in this run", "the same posting as job …"). Settled means applied,
  would apply, or skipped for the job itself (its questions, company or description). A job
  that failed, had no apply button or was external is still tried, and so is its repost.
- A run whose process died without ending it (killed, crashed) no longer stays "in
  progress" in Last runs. It is ended with the counts it last saved and "did not exit
  cleanly" by the first to see it: `ajai status`, the TUI, Telegram's /status, or the next
  run. The next run also ends any older run left without an end, since one run at a time
  holds the machine. A run still alive is never touched.
- A skipped job names each unanswered question once, even when LinkedIn draws it twice.
- An hourly-rate salary question ("valor hora", "valor-hora", "por hora", "R$/hora", "quanto
  você cobra por hora", "hourly rate") gets the hourly figure. It used to get the monthly one,
  so "8000" read as R$ 8,000 an hour.
  - The figure is the monthly desired (or current) salary divided by the monthly basis the
    question names ("baseados em 168 horas"), or by 168 when it names none.
  - Daily or weekly hours are a workload and do not change the rate.
  - A month or a year asked first still wins.
  - These questions are pay questions now, so the AI never answers them.
- A one-page Easy Apply form is no longer submitted within a second. ajai now pauses after
  uploading the resume, so LinkedIn takes the file, and again before pressing Submit, as a
  person reading the Review page would. Both pauses take `pacing.between_actions_min_seconds`
  to `between_actions_max_seconds`, and a stop during either one submits nothing.

### New

- `questions.skill_years`: your years with each technology or role, as "name: years" (for
  example `java: 3`, `.net: 5`, `computação em nuvem: 3`). They answer questions about one
  technology, in English and Portuguese, that ajai used to leave unanswered:
  - "How long have you used X? / Há quanto tempo você usa X? / Quanto tempo de experiência
    com X?" gets X's years (the fewest of those a question names);
  - "Do you have experience with X? / Possui experiência (conhecimento, domínio) com X?"
    gets yes, never no; with a number of years in the question ("at least 5 years", "no
    mínimo 3 anos") only when your years reach it.

  Only the question itself counts, not its context or links. These are left alone, as
  before (memory, AI, review queue):
  - a technology you did not list, or a name inside another word ("java" in "JavaScript",
    "react" in "React-Native");
  - notice periods and totals;
  - options that are ranges of years;
  - sensitive questions.

  Set it in the TUI's Config tab or with
  `ajai config set questions.skill_years "java: 3, .net: 5"`.
- `ajai login`: sign in to LinkedIn once in the profile ajai uses.
- `ajai stop`: stop the run in progress cleanly, from any terminal.
- One run at a time on the machine: a second `ajai run` refuses and names the run in
  progress; `ajai status` shows who started it.
- `ajai doctor` names the Chrome profile and whether LinkedIn is signed in there.
- `ajai schedule add/list/rm/enable/disable`: schedule windows (weekdays, hours, optional
  market and dry run); `ajai status` shows the next opening.
- `ajai daemon`: starts an unattended run when a window opens and stops it when it closes,
  with Telegram notices; `ajai logs --daemon` shows its log, `ajai status` whether it runs.
- `ajai daemon install` / `status` / `uninstall`: the daemon from a systemd user unit tied
  to your desktop session.
- Telegram commands while the daemon runs: `/status`, `/stop`, `/run [market] [dry]`,
  `/help`, from your configured chat only.
- `ajai` with no command opens the TUI in a terminal: a live Dashboard with the run in
  progress, the last runs, pending answers, the next window, the daemon and today's log;
  starting a run (market, dry run or live, a maximum) that runs on its own after the TUI
  closes, and stopping any run; an Answers tab to approve, correct or delete remembered
  answers; a Schedule tab to add, turn on or off and delete schedule windows and to
  install, start, stop or uninstall the daemon's systemd unit; a Jobs tab with the job
  history, filtered and searched, each job a key away from your browser; a Config tab
  that edits every setting (validated, environment overrides marked) and saves the
  secrets in ajai's `.env` from hidden fields; and a setup checklist, opened when
  something a run needs is missing, that runs the init wizard, the LinkedIn sign-in and
  the import from the Python bot. Flame orange marks the active tab (with a stroke under
  it that shows without colour too), the selected row and titles, and the Dashboard
  greets with the ajai logo in a flame gradient, beside or over the summary as the room
  allows. The list tabs share one layout, their filters all in view and the selected
  item's details beside the list on a wide terminal; the Config tab shows paths under
  your home as `~/…`; the Schedule tab shows a window's next openings and what its last
  one did, the daemon in a strip under its title. Keys mean the same on every tab: Tab and
  Shift-Tab switch tabs, ←→ change the filter, Enter edits or opens, `r` `x` `s` work from
  any tab, `n` adds a window and `t` starts or stops the daemon. A live run started from
  the TUI, and a live schedule window added there, are drawn in red and ask once more
  (naming the market and the cap); a dry one goes ahead on Enter.
- `/pending` on Telegram: review pending answers with Approve/Delete buttons, and answer a
  question by replying to its message (checked against the options a select or radio
  offered). Answers remembered from now on keep those options.
