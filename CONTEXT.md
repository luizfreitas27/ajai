# aja

A command-line tool that searches LinkedIn for jobs matching the user's criteria and submits Easy Apply applications on their behalf from the user's own Chrome, optionally drafting answers with an AI model. Everything runs on the user's machine with the user's own account.

## Language

### Jobs and applications

**Job**:
A single LinkedIn job posting, identified by its LinkedIn job ID.
_Avoid_: listing, posting, position, card

**Easy Apply job**:
A job whose application form is LinkedIn's own in-page modal, so the tool can fill and submit it.
_Avoid_: internal job, in-app job

**External job**:
A job whose Apply button leaves LinkedIn for the company's own site. The tool records its link and does not apply.
_Avoid_: redirect job, off-site job

**Applied job**:
A job whose Easy Apply form the tool submitted. Recorded in the applied-jobs history.
_Avoid_: submitted, done

**Skipped job**:
A job the tool deliberately did not apply to because a rule said so: blacklist, bad words, experience, sponsorship, dry run, pacing limit, or an unanswerable question in an unattended run. Not a failure.
_Avoid_: rejected, ignored, filtered

**Failed job**:
A job the tool tried to apply to and could not finish because something broke.
_Avoid_: errored, crashed, skipped

**Dry run**:
A run that fills every form and discards it at the Review step instead of submitting.
_Avoid_: test mode, preview

**Search term**:
One keyword query the tool types into LinkedIn's job search. A run visits every search term in turn.
_Avoid_: query, keyword, role

**Run**:
One full pass over all search terms, ended by the end of the terms, a pacing limit, a schedule window closing, a stop request, or the user. Recorded in the run history. At most one run is in progress on a machine at a time, whichever process started it.
_Avoid_: cycle, loop

**Non-stop session**:
Runs repeated one after another (with `run_non_stop`), ten minutes apart, with the sort order and date filter changed between them, until a run ends in a way that should not be retried (LinkedIn's daily limit, the hourly cap, an error, the user).
_Avoid_: loop, daemon, cycle

**Market**:
The set of values that differ between applying in Brazil and abroad: currency, desired and current salary (monthly in Brazil, annual abroad), search location, search terms and, optionally, the resume to upload. One market is active per run.
_Avoid_: region, locale, country mode, profile

**Pacing**:
The limits that keep a run at a human rhythm: applications per hour and per run, jittered pauses between jobs and between form actions, humanised typing and scrolling. Reaching a pacing limit ends the run cleanly.
_Avoid_: throttling, rate limit, cooldown, stealth

### Form questions

**Question**:
One field in an Easy Apply form, identified by its visible label. Its **control kind** is text, textarea, select (dropdown), radio or checkbox.
_Avoid_: field, input, prompt, control type

**Configured answer**:
An answer the user set in the config for a known kind of question (years of experience, salary, authorization...).
_Avoid_: default, preset, static answer

**Unrecognised question**:
A question whose label matches none of the tool's known kinds, so no configured answer applies.
_Avoid_: unknown, failed question, missing answer

**Answer memory**:
The tool's persistent record of answers it has given to unrecognised questions, and of the questions it could not answer yet, kept so each question has to be resolved once.
_Avoid_: cache, learned answers, Q&A store

**Remembered answer**:
One answer in the answer memory: a question (normalised label plus control kind), the answer text, where it came from (the AI, the user, or a form control the tool saw and left alone) and its review state. A question nothing could answer is remembered with no answer yet, so the user can supply one; until then it answers nothing.
_Avoid_: memory entry, record, saved answer, learned answer

**Review state**:
Whether a remembered answer is **pending** (not yet confirmed by the user) or **approved** (the user confirmed or corrected it). Only approved answers may tick a checkbox.
_Avoid_: status, verified, confirmed

**Review queue**:
The list of pending remembered answers the user works through, approving, correcting or deleting each one, in the terminal (`aja answers review`) or from Telegram. It also holds the questions nothing could answer, waiting for the user's answer.
_Avoid_: inbox, backlog, pending list

**Help pause**:
The stop the tool makes, in an attended run, when required questions have no answer: it prints them, notifies the user, and waits (up to 15 minutes) for them to fill the form in Chrome and confirm in the terminal before continuing. With no confirmation, the job is skipped and the questions go to the review queue.
_Avoid_: dialog, help needed popup, manual mode

**Captured answer**:
A remembered answer taken from what the user typed into the form by hand during a help pause. Approved on creation.
_Avoid_: manual answer, learned from user

**Unattended run**:
A run where nobody is at the terminal (every run the daemon starts, `--unattended`, `--json`, or a terminal missing on either end): no help pause; a job with an unanswerable question is skipped and the question goes to the review queue.
_Avoid_: background mode, headless, silent

**Sensitive question**:
A question about work authorization, visa, citizenship, security clearance, salary, disability or veteran status. Answered only from configured answers, never by the AI or the answer memory.
_Avoid_: protected question, legal question

**Candidate profile**:
The description of the applicant the tool hands to the AI, assembled from the configured personal and application answers plus the free-text `about`. Excludes phone, email, street address, citizenship status and current salary.
_Avoid_: user info, context, persona

### Scheduling and notifications

**Schedule window**:
A span of local time on chosen weekdays, optionally with its own market and dry run. When it opens the scheduler starts one unattended run (a non-stop session if `run_non_stop` is on); when it closes the run is stopped. A run that ends early is not restarted in the same window, and a window that opens while another run is in progress is skipped with a notice.
_Avoid_: cron, timer, slot

**Scheduler**:
The part of the daemon that watches the clock and starts and stops runs for the schedule windows.
_Avoid_: cron job, timer, background worker

**Daemon**:
The long-lived `aja daemon` process, installed as a systemd user service of the user's desktop session, that hosts the scheduler and the remote commands and runs their runs itself.
_Avoid_: server, service, agent, background mode

**Remote command**:
A Telegram message from the configured chat that the daemon acts on: show the status, stop the run, start a run, or work the review queue. Messages from any other chat are ignored.
_Avoid_: bot command, webhook, chat command

**Stop request**:
A request, from `aja stop`, a remote command or a schedule window closing, that the run in progress end cleanly, as if the user had pressed Ctrl-C at its terminal. Honoured by any run on the machine; the run records the request's reason.
_Avoid_: kill, abort, cancel

**Run summary**:
The counts the tool reports when a run ends: applied, external links collected, failed, skipped, and pending answers.
_Avoid_: report, stats, results

### Running the tool

**Chrome profile**:
The Chrome user-data directory the tool drives, where the LinkedIn session lives. By default the **aja profile**, one the tool owns, so the user's everyday Chrome stays free during runs; it can point at the user's own profile instead. The tool never logs in with a password; the user signs in once in that profile (`aja login`).
_Avoid_: safe mode, guest mode, incognito, session cookie

**Import**:
The one-time copy of settings, answer memory and job history from a Python Auto_job_applier_linkedIn checkout into `aja`. Never copies secrets.
_Avoid_: migration, sync, upgrade

**Blacklist**:
Company names, and words in a job's "About the company" text, that make the tool skip the job.
_Avoid_: blocklist, banned companies
