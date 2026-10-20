---
status: accepted
---

# Recruiter messages: approved by the user, sent only at the end of live runs

After a submitted Easy Apply application, many jobs show the hiring team, and some of
those people have a free **Message** button. A short note to one of them can help, but
the same message sent without a person reading it is the worst case: it goes out in the
user's name with words they never saw. LinkedIn also watches messaging far more closely
than applications, so a bad pattern here costs the account more than a bad application.

We chose to queue recruiter messages and send them only with the user's approval, under
these rules:

- **Opt-in.** `messages.enabled` is off by default. Nothing is queued or sent until the
  user turns it on.
- **Approval first.** A queued message is sent only after the user approves its exact
  text, either one message at a time (`ajai messages approve <id> <digest>`) or the whole
  list at once (`ajai messages approve --all <digest-of-list>`). The digest is of the text
  or list the user was shown, so a changed message or a changed list is refused.
- **Sent from the run, not by a timer.** Sending happens only at the end of a live run,
  through the hook that `launch.Plan.Open` wires, never on a dry run.
- **Capped and paced.** At most `messages.max_per_day` sends per local day (default 3),
  with a jittered pause of `between_min_seconds` to `between_max_seconds` before each one.
- **Free messages only.** The tool clicks the hirer's **Message** button and sends only
  when the message is free: a connection's message, or LinkedIn's free message to an open
  profile (the compose says "Free message"). It never uses Premium or paid InMail
  credits. The optional subject is left empty. Before sending, it reads the hiring team
  again. If the recipient differs, LinkedIn shows a Premium upsell, or the compose would
  use an InMail credit, the message is marked `no_recipient` and not sent.
- **Once per job and recipient.** A message is keyed by job ID and recipient profile URL,
  so the same person is never messaged twice for the same job.
- **Mark before clicking.** A message is saved as `sending` before the send click. A
  crash after that point leaves it `sending`, and it is never sent again.

## Accepted risk

LinkedIn's anti-spam checks look at messaging volume and repetition, and this feature adds
a new kind of traffic to an account that already applies to jobs. The daily cap, the
pauses and the approval step keep that traffic small and reviewed, but they do not remove
it. An opt-in user takes that risk knowingly.

A send that may have gone through but was not confirmed (a crash, a lost page) is marked
`sending` and stays there. We accept that such a message is lost rather than sent a second
time. A missed message costs one contact. A repeated one costs a second message to the
same person, the pattern most likely to get the account flagged.

## Considered options

- **Send as soon as a message is queued.** Rejected: the user would never see the text
  before it goes out.
- **Approval per message only.** Rejected as the only path: a user with a long list needs
  one step to approve it. Kept as the default path; the list digest is the shortcut.
- **Premium or paid InMail messages.** Rejected: they cost money. A free message to an
  open profile is sent, with its optional subject left empty.
- **Retry a `sending` message on the next run.** Rejected: a retry after an unknown result
  is the one path that can send the same message twice.
