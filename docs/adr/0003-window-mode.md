---
status: accepted
---

# A configurable window: offscreen by default, headless as an opt-in

This record changes parts of ADR 0001. ADR 0001 is kept as it was written; where this
record and that one disagree, this one holds.

ADR 0001 chose a visible Chrome and no headless mode, on the reasoning that a visible window
with the user's profile is the least detectable setup. In practice the window gets in the way.
It takes focus and sits on top of the user's other work for the whole run, and the user has to
leave it alone. Headless avoids that, but ADR 0001 rules it out, with good reason: headless
Chrome is easier for LinkedIn to detect.

This fork therefore makes the window configurable with `settings.window`:

- `offscreen` (default): a normal, headful Chrome placed off screen
  (`--window-position=-32000,-32000`, `--window-size=1280,900`). It is the same browser as
  `visible`, with the same profile and the same detection profile, but it does not cover the
  user's screen.
- `minimized`: a normal, headful Chrome, minimized through CDP
  (`Browser.setWindowBounds` with `windowState: minimized`) once its window exists.
- `visible`: the launch ADR 0001 describes.
- `headless`: rod's `--headless=new`. It is opt-in and carries the detection risk named in
  ADR 0001, made worse: a headless Chrome is the easier target. Use it only when the window
  matters more than that risk.

`ajai login` always opens a visible window, whatever `settings.window` says, because the user
signs in by hand.

## What this supersedes in ADR 0001

- "Never headless. The window is visible." The window is offscreen by default, and headless
  is available when the user chooses it.
- The rule that `browser.Options` never run headless outside tests. `Options.Window` now
  selects the mode for every run. `HeadlessForTests` remains as the older spelling for the
  browser-tagged tests.

Nothing else in ADR 0001 changes. The browser is still the installed Chrome, still driven
through go-rod, still the user's or ajai's own profile, still with no stealth library, and
the accepted CDP-level risk stands for every mode.

## Consequences

- The default run is headful, so LinkedIn sees the same browser as before. Only the window
  position changes. Detection is no better or worse than `visible`.
- `headless` gets no extra testing beyond the browser-tagged test that opens it and loads a
  page (`internal/browser/window_browser_test.go`).
- `ajai doctor` shows the mode on its Chrome line, so a run's window is never a surprise.
