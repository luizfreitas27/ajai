//go:build browser

package linkedin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/chrome"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/pacing"
)

// connectScript makes a profile page behave as LinkedIn does for an invite. A
// click on a Connect control (the top card's button or the More menu's item)
// sends the invite at once when MODE is "direct", and otherwise opens the dialog
// in the page's template (DIALOG). Once the invite is sent the control reads
// Pending. The page records on <body>: data-more (More clicked), data-connect
// ("top" or "menu"), data-sent-without-note, data-added-note (must stay empty),
// data-dismissed, data-escaped (the menu closed with Escape) and data-typed
// (any key or text typed, must stay empty).
const connectScript = `<template id="dialog">DIALOG</template>
<script>
const MODE = "MODE";
const PENDING = "Pending, click to withdraw invitation sent to Ana Ribeiro";
const mark = (k, v) => document.body.setAttribute("data-" + k, v);
const menu = document.querySelector('[role="menu"]');
if (menu) menu.hidden = true;
let target = null;
const setPending = () => {
  target.setAttribute("aria-label", PENDING);
  const item = target.closest('[role="menuitem"]');
  if (item) item.querySelector("p").textContent = "Pending";
};
document.addEventListener("click", e => {
  const more = e.target.closest('button[aria-label="More"]');
  if (more) { mark("more", "yes"); if (menu) menu.hidden = false; return; }
  const inDialog = e.target.closest('[role="dialog"]');
  const item = e.target.closest('[role="menuitem"]');
  const inv = e.target.closest('[aria-label$=" to connect"]') || (item && item.querySelector('[aria-label$=" to connect"]'));
  if (inv && !inDialog) {
    e.preventDefault();
    target = inv;
    mark("connect", inv.closest('[role="menu"]') ? "menu" : "top");
    if (MODE === "direct") { setPending(); return; }
    document.body.insertAdjacentHTML("beforeend", document.getElementById("dialog").innerHTML);
    return;
  }
  const button = e.target.closest("button");
  if (!button || !inDialog) return;
  if (button.getAttribute("aria-label") === "Send without a note") {
    e.preventDefault();
    mark("sent-without-note", "yes");
    inDialog.remove();
    setPending();
  } else if (button.textContent.trim() === "Add a note") {
    mark("added-note", "yes");
  } else if (button.getAttribute("aria-label") === "Dismiss") {
    mark("dismissed", "yes");
    inDialog.remove();
  }
}, true);
document.addEventListener("keydown", e => {
  if (e.key === "Escape") { mark("escaped", "yes"); if (menu) menu.hidden = true; return; }
  if (e.key.length === 1) mark("typed", e.key);
});
</script>`

// connectDialogs are the dialogs Connect can open, by MODE.
var connectDialogs = map[string]string{
	"note":  "17_invite_note_dialog.html",
	"email": "18_invite_email_dialog.html",
	"limit": "19_invite_limit_notice.html",
}

// connectPage serves html, a profile page, on a headless Chrome and returns a
// Session whose profiles are on that server. mode is "direct" (a click sends the
// invite) or a key of connectDialogs. A redirect is the path a profile's
// /in/ana-ribeiro-fictional URL is sent to (a checkpoint, or another member).
func connectPage(t *testing.T, html, mode, redirect string) *Session {
	t.Helper()
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	dialog := ""
	if mode != "direct" {
		dialog = readFixture(t, connectDialogs[mode])
	}
	script := strings.NewReplacer("DIALOG", dialog, "MODE", mode).Replace(connectScript)
	page := "<html><body>" + html + script + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirect != "" && r.URL.Path == "/in/ana-ribeiro-fictional" {
			http.Redirect(w, r, redirect, http.StatusFound)
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
	s.profileBase = srv.URL + "/in/"
	return s
}

// withoutConnectItem drops the Connect item from a More menu, as a profile with
// only Follow in it has.
func withoutConnectItem(html string) string {
	return regexp.MustCompile(`(?s)<a role="menuitem"[^>]*/preload/custom-invite/.*?</a>`).ReplaceAllString(html, "")
}

// connectCase runs Connect for ana and checks the result and what the page recorded.
func connectCase(t *testing.T, s *Session, want ConnectResult, clicked map[string]string) {
	t.Helper()
	got, err := s.Connect(context.Background(), ana)
	if err != nil || got != want {
		t.Fatalf("Connect = %q, %v; want %q", got, err, want)
	}
	for key, v := range clicked {
		if r := recorded(t, s, key); r != v {
			t.Errorf("data-%s = %q, want %q", key, r, v)
		}
	}
	if r := recorded(t, s, "typed"); r != "" {
		t.Errorf("the page got typing (%q), an invite carries no note", r)
	}
}

// TestConnectSendsWithoutANoteFromTheTopCard: a Connect button in the top card
// opens the note dialog, and the invite goes out with "Send without a note".
func TestConnectSendsWithoutANoteFromTheTopCard(t *testing.T) {
	s := connectPage(t, readFixture(t, "20_profile_top_connect.html"), "note", "")
	connectCase(t, s, ConnectSent, map[string]string{
		"connect":           "top",
		"sent-without-note": "yes",
		"added-note":        "",
	})
}

// TestConnectSendsFromTheMoreMenu: the Connect item of More sends the invite at
// once, with no dialog, and the item reads Pending afterwards.
func TestConnectSendsFromTheMoreMenu(t *testing.T) {
	s := connectPage(t, readFixture(t, "15_profile_more_connect.html"), "direct", "")
	connectCase(t, s, ConnectSent, map[string]string{
		"more":              "yes",
		"connect":           "menu",
		"sent-without-note": "",
	})
}

// TestConnectLeavesAnInviteAlreadyMade: a 1st-degree profile, and an invite
// already pending (in the top card, or on the More menu), are ConnectAlready
// with nothing clicked.
func TestConnectLeavesAnInviteAlreadyMade(t *testing.T) {
	pending := strings.ReplaceAll(readFixture(t, "20_profile_top_connect.html"),
		`aria-label="Invite Ana Ribeiro to connect"`, `aria-label="Pending, click to withdraw invitation sent to Ana Ribeiro"`)
	menuPending := strings.ReplaceAll(readFixture(t, "15_profile_more_connect.html"),
		`aria-label="Invite Ana Ribeiro to connect"`, `aria-label="Pending, click to withdraw invitation sent to Ana Ribeiro"`)
	for name, s := range map[string]*Session{
		"1st degree":       connectPage(t, readFixture(t, "16_profile_connected.html"), "direct", ""),
		"pending top card": connectPage(t, pending, "direct", ""),
		"pending in More":  connectPage(t, menuPending, "direct", ""),
	} {
		got, err := s.Connect(context.Background(), ana)
		if err != nil || got != ConnectAlready {
			t.Errorf("%s: Connect = %q, %v; want already", name, got, err)
		}
		if r := recorded(t, s, "connect"); r != "" {
			t.Errorf("%s: an invite was clicked (%q)", name, r)
		}
	}
}

// TestConnectDismissesAnEmailDialogAndIsUnavailable: LinkedIn asks for the
// member's email; Connect dismisses the dialog and sends nothing. A profile with
// only Follow (no Connect in the top card or More) is unavailable too.
func TestConnectDismissesAnEmailDialogAndIsUnavailable(t *testing.T) {
	email := connectPage(t, readFixture(t, "20_profile_top_connect.html"), "email", "")
	connectCase(t, email, ConnectUnavailable, map[string]string{
		"connect":           "top",
		"dismissed":         "yes",
		"sent-without-note": "",
	})
	follow := connectPage(t, withoutConnectItem(readFixture(t, "15_profile_more_connect.html")), "direct", "")
	connectCase(t, follow, ConnectUnavailable, map[string]string{
		"more":    "yes",
		"escaped": "yes",
		"connect": "",
	})
}

// TestConnectIsUnavailableWhenTheProfileRedirects: the profile opened is another
// member's, so nothing is clicked.
func TestConnectIsUnavailableWhenTheProfileRedirects(t *testing.T) {
	s := connectPage(t, readFixture(t, "20_profile_top_connect.html"), "direct", "/in/someone-else-fictional")
	connectCase(t, s, ConnectUnavailable, map[string]string{"connect": ""})
}

// TestConnectReportsTheWeeklyLimit: LinkedIn's weekly invitation limit is
// dismissed and reported as ConnectLimited, not as a failure.
func TestConnectReportsTheWeeklyLimit(t *testing.T) {
	s := connectPage(t, readFixture(t, "20_profile_top_connect.html"), "limit", "")
	connectCase(t, s, ConnectLimited, map[string]string{
		"connect":           "top",
		"dismissed":         "yes",
		"sent-without-note": "",
	})
}

// TestConnectStopsAtACheckpoint: a checkpoint page is ConnectBlocked, and
// nothing is clicked on it.
func TestConnectStopsAtACheckpoint(t *testing.T) {
	s := connectPage(t, readFixture(t, "20_profile_top_connect.html"), "direct", "/checkpoint/challenge/")
	connectCase(t, s, ConnectBlocked, map[string]string{"connect": ""})
}

// TestConnectSendsFromAnAnchorOnASecondDegreeProfile: the top card's Connect is
// an anchor to /preload/custom-invite/, and the profile shows a hidden "· 1st"
// beside "· 2nd". Connect clicks the anchor, the dialog is answered, and the
// result is sent, never already.
func TestConnectSendsFromAnAnchorOnASecondDegreeProfile(t *testing.T) {
	s := connectPage(t, readFixture(t, "22_profile_2nd_top_connect_anchor.html"), "note", "")
	connectCase(t, s, ConnectSent, map[string]string{
		"connect":           "top",
		"sent-without-note": "yes",
		"added-note":        "",
	})
}

// TestConnectNeverClicksASidebarInvite: the "Invite … to connect" cards in the
// sidebar stay as they were, the top card's Connect is the one sent.
func TestConnectNeverClicksASidebarInvite(t *testing.T) {
	s := connectPage(t, readFixture(t, "22_profile_2nd_top_connect_anchor.html"), "note", "")
	connectCase(t, s, ConnectSent, map[string]string{"connect": "top"})
	side, err := s.Page.Elements(`//aside//*[@aria-label="Invite Carla Mendes to connect" or @aria-label="Invite Dimitri Lopes to connect"]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(side) != 2 {
		t.Errorf("the sidebar invites changed: %d still read Invite, want 2", len(side))
	}
}

// TestConnectGoesToTheMoreMenuOfAThirdDegreeTopCard: the top card of a
// 3rd-degree profile (fixture 23) has no Connect, so the More menu sends it.
// The menu is the one captured in fixture 15, appended after the top card.
func TestConnectGoesToTheMoreMenuOfAThirdDegreeTopCard(t *testing.T) {
	page := readFixture(t, "23_profile_3rd_top_card.html") + readFixture(t, "15_profile_more_connect.html")
	s := connectPage(t, page, "direct", "")
	connectCase(t, s, ConnectSent, map[string]string{
		"more":              "yes",
		"connect":           "menu",
		"sent-without-note": "",
	})
}
