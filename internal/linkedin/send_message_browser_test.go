//go:build browser

package linkedin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/chrome"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/pacing"
)

// overlayScript makes a captured hiring team (07, or the search pane's 11)
// behave as LinkedIn's does: the Message link or button opens the overlay in
// place, in the open shadow root of #interop-outlet, from the template, drawn
// over the whole page as long as it is open. The page records on <body> what
// happened (data-opened, data-sends, data-sent-text, data-closed,
// data-dismissed, data-next-card for a click on the next job card). A send
// clears the box and lists the message, unless MODE is "silent"; MODE "draft"
// leaves a draft in the box.
const overlayScript = `<div id="interop-outlet" data-testid="interop-shadowdom"></div>
<template id="overlay">OVERLAY</template>
<script>
const MODE = "SENDMODE";
const mark = (k, v) => document.body.setAttribute("data-" + k, v);
document.addEventListener("click", e => {
  if (e.target.closest && e.target.closest("li[data-occludable-job-id] a")) { e.preventDefault(); mark("next-card", "yes"); }
}, true);
document.addEventListener("click", e => {
  const link = e.target.closest && e.target.closest('a[aria-label="Message"], button:has([data-test-icon="send-privately-small"])');
  if (!link) return;
  e.preventDefault();
  mark("opened", "yes");
  const host = document.getElementById("interop-outlet");
  const root = host.shadowRoot || host.attachShadow({mode: "open"});
  root.innerHTML = '<style>[role="dialog"], [data-test-modal-id] { position: fixed; inset: 0; z-index: 999; background: #fff; }</style>' +
    '<aside id="msg-overlay">' + document.getElementById("overlay").innerHTML + '</aside>';
  const box = root.querySelector(".msg-form__contenteditable");
  const send = root.querySelector(".msg-form__send-btn");
  const form = root.querySelector("form.msg-form");
  const bubble = root.querySelector('[role="dialog"]');
  if (box && MODE === "draft") box.innerText = "Draft: ";
  if (box) box.addEventListener("input", () => { send.disabled = box.innerText.trim() === ""; });
  // MODE "late": the first keystroke makes the compose an InMail, as if LinkedIn
  // showed its credits only once the box was in use.
  if (box && MODE === "late") box.addEventListener("input", () => {
    if (bubble.querySelector(".msg-inmail-credits-display")) return;
    bubble.insertAdjacentHTML("afterbegin", '<section class="msg-inmail-credits-display">Use 1 of 5 InMail credits</section><input name="subject">');
    mark("late", "yes");
  });
  if (form) form.addEventListener("submit", ev => {
    ev.preventDefault();
    mark("sends", String(Number(document.body.dataset.sends || 0) + 1));
    mark("sent-text", box.innerText);
    if (MODE === "silent") return;
    const sent = document.createElement("p");
    sent.className = "msg-s-event-listitem__body";
    sent.textContent = box.innerText.trim();
    bubble.appendChild(sent);
    box.innerHTML = "<p><br></p>";
    send.disabled = true;
  });
  root.addEventListener("click", ev => {
    const b = ev.target.closest("button");
    if (!b) return;
    if (b.hasAttribute("data-test-modal-close-btn")) { mark("dismissed", "yes"); b.closest("[data-test-modal-id]").remove(); }
    if (b.querySelector('[data-test-icon="close-small"]')) { mark("closed", "yes"); bubble.remove(); }
  });
}, true);
</script>`

// nextCard is a job card listed after the job shown, for checking the list is
// still usable once a message went out.
const nextCard = `<ul><li data-occludable-job-id="4100000002"><div class="artdeco-entity-lockup__title"><a href="/jobs/view/4100000002/">Next job</a></div></li></ul>`

// messagePage serves fixture 07 with the overlay from overlayFixture (edited by
// replace) at every /jobs/view/ path and returns a Session on a headless Chrome
// whose job pages are on that server. A blocked server sends the job page to a
// checkpoint instead.
func messagePage(t *testing.T, overlayFixture, mode string, blocked bool, replace ...string) *Session {
	t.Helper()
	return overlayPage(t, "07_hiring_team.html", overlayFixture, mode, blocked, replace...)
}

// overlayPage is messagePage over pageFixture, with nextCard listed above it.
func overlayPage(t *testing.T, pageFixture, overlayFixture, mode string, blocked bool, replace ...string) *Session {
	t.Helper()
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	overlay := read(overlayFixture)
	for i := 0; i+1 < len(replace); i += 2 {
		overlay = strings.ReplaceAll(overlay, replace[i], replace[i+1])
	}
	script := strings.NewReplacer("OVERLAY", overlay, "SENDMODE", mode).Replace(overlayScript)
	page := "<html><body>" + nextCard + read(pageFixture) + script + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blocked && strings.HasPrefix(r.URL.Path, "/jobs/view/") {
			http.Redirect(w, r, "/checkpoint/challenge/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	br, err := browser.Open(ctx, browser.Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true,
		Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Close() })
	s := NewSession(br.Main(), br, pacing.New(config.Pacing{}, true), nil)
	s.jobBase = srv.URL + "/jobs/view/"
	return s
}

// recorded is what the test page wrote on <body> under data-<key>, "" when nothing.
func recorded(t *testing.T, s *Session, key string) string {
	t.Helper()
	body, err := s.Page.Element("//body")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := body.Attr("data-" + key)
	return v
}

var ana = messages.Recipient{Name: "Ana Ribeiro", URL: "https://www.linkedin.com/in/ana-ribeiro-fictional"}

const note = "Olá Ana, I just applied to the Go role.\nThank you!"

// TestSendMessageSendsAFreeMessage: a free InMail to an open profile and a
// connection's plain compose are typed exactly, sent once and closed.
func TestSendMessageSendsAFreeMessage(t *testing.T) {
	for name, s := range map[string]*Session{
		"free InMail": messagePage(t, "08_message_overlay.html", "", false),
		"plain compose": messagePage(t, "08_message_overlay.html", "", false,
			"msg-inmail-credits-display", "gone", `name="subject"`, `name="other"`),
	} {
		got, err := s.SendMessage(context.Background(), ana, "4100000001", note)
		if err != nil || got != SendSent {
			t.Errorf("%s: SendMessage = %q, %v; want sent", name, got, err)
		}
		if n := recorded(t, s, "sends"); n != "1" {
			t.Errorf("%s: send clicked %q times, want 1", name, n)
		}
		if sent := recorded(t, s, "sent-text"); strings.Join(strings.Fields(sent), " ") != strings.Join(strings.Fields(note), " ") {
			t.Errorf("%s: the box held %q when sent, want %q", name, sent, note)
		}
		if recorded(t, s, "closed") != "yes" {
			t.Errorf("%s: the bubble was left open", name)
		}
	}
}

// TestSendMessageNeverPays: a Premium upsell is dismissed, an InMail that would
// use a paid credit is closed, and neither is sent.
func TestSendMessageNeverPays(t *testing.T) {
	upsell := messagePage(t, "09_inmail_compose.html", "", false)
	paid := messagePage(t, "08_message_overlay.html", "", false, "Free message", "Use 1 of 5 InMail credits")
	for name, c := range map[string]struct {
		s     *Session
		after string
	}{"premium upsell": {upsell, "dismissed"}, "paid InMail": {paid, "closed"}} {
		got, err := c.s.SendMessage(context.Background(), ana, "4100000001", note)
		if err != nil || got != SendNoRecipient {
			t.Errorf("%s: SendMessage = %q, %v; want no recipient", name, got, err)
		}
		if n := recorded(t, c.s, "sends"); n != "" {
			t.Errorf("%s: send clicked %q times", name, n)
		}
		if recorded(t, c.s, c.after) != "yes" {
			t.Errorf("%s: not %s", name, c.after)
		}
	}
}

// TestSendMessageRechecksFreeBeforeSending: a compose that shows as free when it
// opens, and an InMail once typing starts, is closed unsent.
func TestSendMessageRechecksFreeBeforeSending(t *testing.T) {
	s := messagePage(t, "08_message_overlay.html", "late", false,
		"msg-inmail-credits-display", "gone", `name="subject"`, `name="other"`)
	got, err := s.SendMessage(context.Background(), ana, "4100000001", note)
	if err != nil || got != SendNoRecipient {
		t.Errorf("SendMessage = %q, %v; want no recipient", got, err)
	}
	if recorded(t, s, "late") != "yes" {
		t.Error("the InMail marker was never injected, so the recheck was not tested")
	}
	if n := recorded(t, s, "sends"); n != "" {
		t.Errorf("send clicked %q times", n)
	}
	if recorded(t, s, "closed") != "yes" {
		t.Error("the bubble was left open")
	}
}

// TestSendMessageNeedsTheRecipientLink: a compose without the profile link to the
// hirer is not theirs to send to; nothing is sent.
func TestSendMessageNeedsTheRecipientLink(t *testing.T) {
	defer func(d time.Duration) { composeWait = d }(composeWait)
	composeWait = 2 * time.Second
	s := messagePage(t, "08_message_overlay.html", "", false, "msg-compose__profile-link", "gone")
	got, err := s.SendMessage(context.Background(), ana, "4100000001", note)
	if err != nil || got != SendNoRecipient {
		t.Errorf("SendMessage = %q, %v; want no recipient", got, err)
	}
	if n := recorded(t, s, "sends"); n != "" {
		t.Errorf("send clicked %q times", n)
	}
}

// TestSendMessageToSomeoneElse never opens a compose when the hirer changed.
func TestSendMessageToSomeoneElse(t *testing.T) {
	s := messagePage(t, "08_message_overlay.html", "", false)
	other := messages.Recipient{Name: "Ana Ribeiro", URL: "https://www.linkedin.com/in/someone-else-fictional"}
	got, err := s.SendMessage(context.Background(), other, "4100000001", note)
	if err != nil || got != SendNoRecipient {
		t.Errorf("SendMessage = %q, %v; want no recipient", got, err)
	}
	if recorded(t, s, "opened") != "" {
		t.Error("the compose was opened for another hirer")
	}
}

// TestSendMessageOnACheckpoint reports a block and opens nothing.
func TestSendMessageOnACheckpoint(t *testing.T) {
	s := messagePage(t, "08_message_overlay.html", "", true)
	got, err := s.SendMessage(context.Background(), ana, "4100000001", note)
	if err != nil || got != SendBlocked {
		t.Errorf("SendMessage = %q, %v; want blocked", got, err)
	}
}

// TestSendMessageWhenTheBoxHoldsOtherText fails without sending: a draft left in
// the box would go out with the message.
func TestSendMessageWhenTheBoxHoldsOtherText(t *testing.T) {
	s := messagePage(t, "08_message_overlay.html", "draft", false)
	if got, err := s.SendMessage(context.Background(), ana, "4100000001", note); err == nil {
		t.Errorf("SendMessage = %q with no error; want an error", got)
	}
	if n := recorded(t, s, "sends"); n != "" {
		t.Errorf("send clicked %q times", n)
	}
}

// TestSendMessageWithoutConfirmation fails after the one click when LinkedIn
// never shows the message sent.
func TestSendMessageWithoutConfirmation(t *testing.T) {
	defer func(d time.Duration) { sentWait = d }(sentWait)
	sentWait = 2 * time.Second
	s := messagePage(t, "08_message_overlay.html", "silent", false)
	if got, err := s.SendMessage(context.Background(), ana, "4100000001", note); err == nil {
		t.Errorf("SendMessage = %q with no error; want an error", got)
	}
	if n := recorded(t, s, "sends"); n != "1" {
		t.Errorf("send clicked %q times, want 1", n)
	}
}
