---
status: accepted
---

# go-rod attached to the user's own Chrome, with no stealth library

`aja` must be a single Go binary, and it must drive a visible Chrome with the user's own
profile. In Go the realistic options are go-rod and chromedp, both plain CDP clients.
go-rod's companion stealth package has been unmaintained since May 2023 and only patches
first-generation signals (navigator.webdriver, plugins, WebGL strings); chromedp ships
none. Nothing in Go matches SeleniumBase UC Mode or nodriver, which the Python version
used, and a May 2026 anti-detection benchmark did not include a single Go tool.

We chose **go-rod in user mode**, connecting to the Chrome already installed with the
user's real profile, and we deliberately use **no stealth library**. The mitigation is
architectural and behavioural rather than a patch set:

- Chrome itself makes every request to LinkedIn, so TLS, HTTP/2 and header fingerprints are
  a real browser's. The Go process never talks to LinkedIn.
- Never headless. The window is visible, the profile persistent, the session reused.
- The user logs in by hand once; `aja` never types a password.
- Conservative pacing is on by default (`pacing` in the config): applications per hour and
  per run are capped, there are jittered pauses between jobs and between form actions,
  typing and scrolling are humanised, and there is no parallelism.

## Accepted risk

CDP-level fingerprinting (the `Runtime.enable` leak, binding-context anomalies) is not
mitigated. Only the Python nodriver/zendriver lineage defends against it today. If LinkedIn
starts detecting CDP itself, the options are a Python sidecar for the browser layer (giving
up the single binary) or driving Chrome through OS-level input. Revisit this ADR then.

## Considered options

- **chromedp**: same detection profile, lower-level API, no advantage.
- **go-rod + go-rod/stealth**: abandoned patches that may themselves be a signal.
- **Python sidecar with SeleniumBase UC**: best known stealth, but requires Python on the
  user's machine, which defeats the single-binary goal.
