# Changelog

Release notes, newest first. When a `v*` tag is pushed, the release workflow publishes the
section whose heading is that tag (rename "Unreleased" to it first, e.g. `## v0.2.0`); with
no such section the notes are the commit list.

## Unreleased

### Upgrading from phase 1

- **aja now uses a Chrome profile of its own.** With `settings.chrome_profile_dir` blank
  (the default) runs use the aja profile in the data directory, so your everyday Chrome
  stays open and free. Run `aja login` once to sign in to LinkedIn there, or set
  `settings.chrome_profile_dir` to your everyday profile (`aja doctor` prints its path) to
  keep the old behaviour. On the aja profile a run closes its Chrome when it ends; on yours
  it leaves the window open, as before.
- **Linux only for now.** Releases are built for linux/amd64 and linux/arm64.

### New

- `aja login`: sign in to LinkedIn once in the profile aja uses.
- `aja stop`: stop the run in progress cleanly, from any terminal.
- One run at a time on the machine: a second `aja run` refuses and names the run in
  progress; `aja status` shows who started it.
- `aja doctor` names the Chrome profile and whether LinkedIn is signed in there.
- `aja schedule add/list/rm/enable/disable`: schedule windows (weekdays, hours, optional
  market and dry run); `aja status` shows the next opening.
- `aja daemon`: starts an unattended run when a window opens and stops it when it closes,
  with Telegram notices; `aja logs --daemon` shows its log, `aja status` whether it runs.
- `aja daemon install` / `status` / `uninstall`: the daemon from a systemd user unit tied
  to your desktop session.
- Telegram commands while the daemon runs: `/status`, `/stop`, `/run [market] [dry]`,
  `/help`, from your configured chat only.
- `/pending` on Telegram: review pending answers with Approve/Delete buttons, and answer a
  question by replying to its message (checked against the options a select or radio
  offered). Answers remembered from now on keep those options.
