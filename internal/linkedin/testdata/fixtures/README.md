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
