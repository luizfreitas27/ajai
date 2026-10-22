LinkedIn markup captured from a live logged-in session on 2026-09-09, copied from the
Python project's `tests/fixtures`. `selectors_test.go` asserts that every locator in
`selectors.go` still matches these pages. When LinkedIn moves again, recapture the
fixtures and the test says exactly which locators died.

Not captured yet: a form page LinkedIn refused. `InlineErrorXPath`
(`artdeco-inline-feedback--error`, the red message under a question, "Please enter a valid
answer") is only checked not to match a clean form; capture a refused page (a required
question left empty, then Next) and check it matches there.

Also not captured: a conversation after a send (`SentMessageBodyXPath`,
`msg-s-event-listitem__body`; the send is confirmed by the box emptying as well), a
connection's compose (no subject, no credits line) and a checkpoint page (`SendMessage`
reads `/checkpoint/` in the URL).

`07_hiring_team.html` is the "People you can reach out to" slot (`JobDetailsPeopleWhoCanHelpSlot_<id>`) of a job with a hiring team: the hirer card, its `/in/<slug>` link and the `aria-label="Message"` link to `/messaging/compose/`, names and ids fictional. `10_no_hiring_team.html` is the job details block (About the job, About the company) of a job whose slot is empty, with the description text and the similar-jobs cards replaced by fictional text. `08_message_overlay.html` is the compose bubble (`role="dialog"`, `msg-overlay-conversation-bubble`) that the hirer's Message link opens in the open shadow root of `#interop-outlet`, before anything is typed: a free InMail with the optional subject input, the `Write a message…` textbox and the send button still disabled, names fictional. `09_inmail_compose.html` is what that link opens instead for a hirer the account cannot message for free: the Premium upsell modal (`data-test-modal-id="modal-upsell"`, "Message Ana with Premium"), also inside that shadow root, with the account owner's name and photo removed.

`11_search_pane_hiring_team.html` is the hiring team as the search page's details pane draws it (captured 2026-10-20), where ajai reads it right after applying: an older layout with no `JobDetailsPeopleWhoCanHelpSlot_` id (`job-details-people-who-can-help__section--two-pane`, "Meet the hiring team" in an `h2`), two `/in/<slug>` links per hirer (photo and name) and a Message `button` with no aria-label or href, marked by its `data-test-icon="send-privately-small"` icon; its ancestors up to `jobs-search__job-details`, the description cut to one fictional line, names fictional, the photo removed.

`12_daily_limit_notice.html` is synthetic (fictional company and job id), written on
2026-10-20 from a screenshot of a live run: the job page at LinkedIn's daily Easy Apply
limit, the Easy Apply button disabled and a red notice under it ("We limit daily
submissions … Save this job and apply tomorrow."). Its real markup was not captured;
when it shows again, capture it and check `DailyLimitXPath` still matches.

`13_search_upsell.html` is the Premium upsell as the jobs search page drew it on 2026-10-21, after the Message link of a hirer was clicked there: `modal-upsell` in the main document, not in a shadow root, over the job list. The search page has no `#interop-outlet` at all, so `SendHere` finds the upsell in the document. Names fictional ("Ana", "Rafael"), the owner's photo and the profile image URLs removed, the job id and the referenceId in the Premium link replaced. `selectors_test.go` checks that `UpsellModalXPath` and `UpsellDismissXPath` still match it.

Connect (`connect.go`, `connect_browser_test.go`) reads a member's profile page. The
files below are not captured from a live page unless the entry says so.

`15_profile_more_connect.html` is a 3rd-degree recruiter's profile (captured 2026-10-21): the
More menu is `more_menu.html` as captured, with the name, slug and member URNs replaced
(Ana Ribeiro, ana-ribeiro-fictional), and its Connect item is `/preload/custom-invite/`
with `aria-label="Invite Ana Ribeiro to connect"`. The top card above it is synthetic: the
Message link, Follow and More, the degree text "· 3rd", no h1 (as on LinkedIn).

`16_profile_connected.html` is a 1st-degree profile: the actions markup of the captured
`profile_connected.html` (Message link and More button, degree "· 1st") reduced to its
top card, the name fictional. No Connect control.

`20_profile_top_connect.html` is synthetic, not captured: a 3rd-degree top card whose
Connect is a button in the card (`aria-label="Invite Ana Ribeiro to connect"`), as on
some profiles. Message, Connect and More; no More menu.

`17_invite_note_dialog.html` is synthetic, not captured: the "Add a note to your
invitation?" dialog, with "Add a note" and `aria-label="Send without a note"`.

`18_invite_email_dialog.html` is synthetic, not captured: the dialog that asks for the
member's email ("To verify this member knows you, please enter their email address.").

`19_invite_limit_notice.html` is synthetic, not captured: the weekly invitation limit notice
("You've reached the weekly invitation limit."). Its dismiss button is the `Dismiss` close
control.

The Pending state (`aria-label="Pending, click to withdraw invitation sent to Ana Ribeiro"` on
the control, "Pending" on the menu item) is synthetic in every fixture: it is the test page's
own relabelling after a send, and the test edits 15 and 20 to show it. Capture a real sent
invite (and the three dialogs above) and check `InviteEmailDialogXPath`,
`InviteLimitDialogXPath`, `InviteSendWithoutNoteXPath` and `TopCardPendingXPath` still match.
