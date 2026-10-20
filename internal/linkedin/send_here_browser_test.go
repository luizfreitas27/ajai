//go:build browser

package linkedin

import (
	"context"
	"strings"
	"testing"

	"github.com/luizfreitas27/ajai/internal/messages"
)

// panePage is the search page right after an application: the next job card
// and the details pane's hiring team (11), whose Message button opens the
// overlay from overlayFixture in place.
func panePage(t *testing.T, overlayFixture, mode string, replace ...string) *Session {
	t.Helper()
	s := overlayPage(t, "11_search_pane_hiring_team.html", overlayFixture, mode, false, replace...)
	if err := s.Page.Navigate(context.Background(), strings.TrimSuffix(s.jobBase, "/jobs/view/")+"/jobs/search/"); err != nil {
		t.Fatal(err)
	}
	return s
}

var bruno = messages.Recipient{Name: "Bruno Tavares", URL: "https://www.linkedin.com/in/bruno-tavares-fictional"}

// TestSendHereSendsFromTheSearchPaneAndLeavesTheListUsable: the message is typed
// exactly and sent once without leaving the page, the bubble is closed, and the
// next card can be clicked.
func TestSendHereSendsFromTheSearchPaneAndLeavesTheListUsable(t *testing.T) {
	s := panePage(t, "08_message_overlay.html", "", "ana-ribeiro-fictional", "bruno-tavares-fictional")
	before, _ := s.Page.URL()
	got, err := s.SendHere(context.Background(), bruno, note)
	if err != nil || got != SendSent {
		t.Fatalf("SendHere = %q, %v; want sent", got, err)
	}
	if after, _ := s.Page.URL(); after != before {
		t.Errorf("the page moved from %s to %s", before, after)
	}
	if n := recorded(t, s, "sends"); n != "1" {
		t.Errorf("send clicked %q times, want 1", n)
	}
	if sent := recorded(t, s, "sent-text"); strings.Join(strings.Fields(sent), " ") != strings.Join(strings.Fields(note), " ") {
		t.Errorf("the box held %q when sent, want %q", sent, note)
	}
	host, err := s.Page.Element(MessageOutletXPath)
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := host.ShadowElements(ComposeBubbleXPath); len(left) != 0 {
		t.Errorf("%d compose bubbles left open", len(left))
	}
	cards, err := s.Cards()
	if err != nil || len(cards) != 1 {
		t.Fatalf("cards %d, %v", len(cards), err)
	}
	if err := s.OpenCard(context.Background(), cards[0]); err != nil {
		t.Errorf("the next card could not be opened: %v", err)
	}
	if recorded(t, s, "next-card") != "yes" {
		t.Error("the click on the next card never reached it")
	}
}

// TestSendHereNeverPays: the Premium upsell is dismissed, a compose that turns
// into a paid InMail once typing starts is closed, and neither is sent.
func TestSendHereNeverPays(t *testing.T) {
	upsell := panePage(t, "09_inmail_compose.html", "")
	late := panePage(t, "08_message_overlay.html", "late", "ana-ribeiro-fictional", "bruno-tavares-fictional",
		"msg-inmail-credits-display", "gone", `name="subject"`, `name="other"`)
	for name, c := range map[string]struct {
		s     *Session
		after string
	}{"premium upsell": {upsell, "dismissed"}, "paid markers drawn late": {late, "late"}} {
		got, err := c.s.SendHere(context.Background(), bruno, note)
		if err != nil || got != SendNoRecipient {
			t.Errorf("%s: SendHere = %q, %v; want no recipient", name, got, err)
		}
		if n := recorded(t, c.s, "sends"); n != "" {
			t.Errorf("%s: send clicked %q times", name, n)
		}
		if recorded(t, c.s, c.after) != "yes" {
			t.Errorf("%s: not %s", name, c.after)
		}
	}
}

// TestSendHereToSomeoneElse never opens a compose when the pane shows another hirer.
func TestSendHereToSomeoneElse(t *testing.T) {
	s := panePage(t, "08_message_overlay.html", "")
	other := messages.Recipient{Name: "Bruno Tavares", URL: "https://www.linkedin.com/in/someone-else-fictional"}
	got, err := s.SendHere(context.Background(), other, note)
	if err != nil || got != SendNoRecipient {
		t.Errorf("SendHere = %q, %v; want no recipient", got, err)
	}
	if recorded(t, s, "opened") != "" {
		t.Error("the compose was opened for another hirer")
	}
}

// TestSendHereLeavesNoBubbleWhenTheSendNeverShowsAsSent: the send is clicked
// but LinkedIn shows nothing sent. That is an error, the bubble is closed, and
// the next card still opens.
func TestSendHereLeavesNoBubbleWhenTheSendNeverShowsAsSent(t *testing.T) {
	s := panePage(t, "08_message_overlay.html", "silent", "ana-ribeiro-fictional", "bruno-tavares-fictional")
	got, err := s.SendHere(context.Background(), bruno, note)
	if err == nil {
		t.Fatalf("SendHere = %q, nil; want an error: the send was clicked and never showed as sent", got)
	}
	if n := recorded(t, s, "sends"); n != "1" {
		t.Errorf("send clicked %q times, want 1", n)
	}
	host, err := s.Page.Element(MessageOutletXPath)
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := host.ShadowElements(ComposeBubbleXPath); len(left) != 0 {
		t.Errorf("%d compose bubbles left open on the search page", len(left))
	}
	cards, err := s.Cards()
	if err != nil || len(cards) != 1 {
		t.Fatalf("cards %d, %v", len(cards), err)
	}
	if err := s.OpenCard(context.Background(), cards[0]); err != nil {
		t.Errorf("the next card could not be opened: %v", err)
	}
	if recorded(t, s, "next-card") != "yes" {
		t.Error("the click on the next card never reached it")
	}
}
