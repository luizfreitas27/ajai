LinkedIn markup captured from a live logged-in session on 2026-09-09, copied from the
Python project's `tests/fixtures`. `selectors_test.go` asserts that every locator in
`selectors.go` still matches these pages. When LinkedIn moves again, recapture the
fixtures and the test says exactly which locators died.

Not captured yet: a form page LinkedIn refused. `InlineErrorXPath`
(`artdeco-inline-feedback--error`, the red message under a question, "Please enter a valid
answer") is only checked not to match a clean form; capture a refused page (a required
question left empty, then Next) and check it matches there.

`07_daily_limit_notice.html` is synthetic (fictional company and job id), written on
2026-10-20 from a screenshot of a live run: the job page at LinkedIn's daily Easy Apply
limit, the Easy Apply button disabled and a red notice under it ("We limit daily
submissions … Save this job and apply tomorrow."). Its real markup was not captured;
when it shows again, capture it and check `DailyLimitXPath` still matches.
