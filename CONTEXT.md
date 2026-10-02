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
One full pass over all search terms, ended by the end of the terms, a pacing limit, a schedule window closing, or the user. Recorded in the run history.
_Avoid_: session, cycle, loop

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
The tool's persistent record of answers it has given to unrecognised questions, kept so each question has to be resolved once.
_Avoid_: cache, learned answers, Q&A store

**Remembered answer**:
One answer in the answer memory: a question (normalised label plus control kind), the answer text, where it came from (the AI, the user, or a form control the tool saw and left alone) and its review state.
_Avoid_: memory entry, record, saved answer, learned answer

**Review state**:
Whether a remembered answer is **pending** (not yet confirmed by the user) or **approved** (the user confirmed or corrected it). Only approved answers may tick a checkbox.
_Avoid_: status, verified, confirmed

**Review queue**:
The list of pending remembered answers the user works through, approving, correcting or deleting each one, in the terminal or the control panel.
_Avoid_: inbox, backlog, pending list

**Help pause**:
The stop the tool makes, in an attended run, when a question has no answer: it prints the question, notifies the user, and waits for them to fill the form in Chrome and confirm in the terminal before continuing.
_Avoid_: dialog, help needed popup, manual mode

**Captured answer**:
A remembered answer taken from what the user typed into the form by hand during a help pause. Approved on creation.
_Avoid_: manual answer, learned from user

**Unattended run**:
A run where nobody is at the terminal (the panel, the scheduler, `--unattended`): no help pause; a job with an unanswerable question is skipped and the question remembered as pending.
_Avoid_: background mode, headless, silent

**Sensitive question**:
A question about work authorization, visa, citizenship, security clearance, salary, disability or veteran status. Answered only from configured answers, never by the AI or the answer memory.
_Avoid_: protected question, legal question

**Candidate profile**:
The description of the applicant the tool hands to the AI, assembled from the configured personal and application answers plus the free-text `about`. Excludes phone, email, street address, citizenship status and current salary.
_Avoid_: user info, context, persona

### Scheduling and notifications

**Schedule window**:
A weekday-and-time span during which the scheduler starts a run on its own and stops it at the end.
_Avoid_: cron, timer, slot

**Scheduler**:
The long-lived `aja` process (`aja schedule daemon`) that watches the clock and starts and stops runs for the schedule windows.
_Avoid_: cron job, service, background worker

**Run summary**:
The counts the tool reports when a run ends: applied, external links collected, failed, skipped, and pending answers.
_Avoid_: report, stats, results

### Running the tool

**Control panel**:
The local web page served by `aja panel` where the user edits settings, starts and stops runs, works the review queue and views history.
_Avoid_: dashboard, UI, web app, admin

**Chrome profile**:
The user's own Chrome user-data directory that the tool attaches to, where the LinkedIn session lives. The tool never logs in with a password; the user logs in once in that profile.
_Avoid_: safe mode, guest mode, incognito, session cookie

**Import**:
The one-time copy of settings, answer memory and job history from a Python Auto_job_applier_linkedIn checkout into `aja`. Never copies secrets.
_Avoid_: migration, sync, upgrade

**Blacklist**:
Company names, and words in a job's "About the company" text, that make the tool skip the job.
_Avoid_: blocklist, banned companies
