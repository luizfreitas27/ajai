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
	"github.com/luizfreitas27/ajai/internal/messages"
)

// hiringTeamPage serves a captured job details page (or the fixture with
// replace applied to it) and returns a Session on it in a headless Chrome.
func hiringTeamPage(t *testing.T, fixtureName string, replace ...string) *Session {
	t.Helper()
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	body, err := os.ReadFile(filepath.Join("testdata", "fixtures", fixtureName))
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for i := 0; i+1 < len(replace); i += 2 {
		page = strings.ReplaceAll(page, replace[i], replace[i+1])
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	pg := br.Main()
	if err := pg.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	return NewSession(pg, br, nil, nil)
}

// TestHiringTeamReadsTheHirerWithAFreeMessageButton reads the captured hiring
// team in a real Chrome: the hirer's name and profile, and nothing clicked.
func TestHiringTeamReadsTheHirerWithAFreeMessageButton(t *testing.T) {
	s := hiringTeamPage(t, "07_hiring_team.html")
	before, _ := s.Page.URL()
	got, ok, err := s.HiringTeam(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != (messages.Recipient{Name: "Ana Ribeiro", URL: "https://www.linkedin.com/in/ana-ribeiro-fictional"}) {
		t.Errorf("HiringTeam = %+v, %v", got, ok)
	}
	if after, _ := s.Page.URL(); after != before {
		t.Errorf("the page moved from %s to %s: something was clicked", before, after)
	}
}

// TestHiringTeamReadsTheSearchPane reads the hirer from the search page's
// details pane, which ajai is on right after an application.
func TestHiringTeamReadsTheSearchPane(t *testing.T) {
	s := hiringTeamPage(t, "11_search_pane_hiring_team.html")
	got, ok, err := s.HiringTeam(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != (messages.Recipient{Name: "Bruno Tavares", URL: "https://www.linkedin.com/in/bruno-tavares-fictional"}) {
		t.Errorf("HiringTeam = %+v, %v", got, ok)
	}
}

// TestHiringTeamReadsThePaneWhenItsFirstSectionIsTheConnectionsCard reads the
// hirer of the pane's hiring team when the section before it is "People you can
// reach out to", which holds no hirer, and the Message control is a text button.
func TestHiringTeamReadsThePaneWhenItsFirstSectionIsTheConnectionsCard(t *testing.T) {
	s := hiringTeamPage(t, "14_search_pane_text_message_button.html")
	got, ok, err := s.HiringTeam(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != (messages.Recipient{Name: "Bruno Tavares", URL: "https://www.linkedin.com/in/bruno-tavares-fictional"}) {
		t.Errorf("HiringTeam = %+v, %v", got, ok)
	}
}

// TestHiringTeamSkipsAHirerWithoutAMessageButton never pairs a hirer with the
// next hirer's button: the first hirer with a button of their own is read.
func TestHiringTeamSkipsAHirerWithoutAMessageButton(t *testing.T) {
	card := `<div class="display-flex align-items-center mt4">`
	s := hiringTeamPage(t, "11_search_pane_hiring_team.html", card, card+
		`<a href="https://www.linkedin.com/in/carla-mendes-fictional"><strong>Carla Mendes</strong></a></div>`+card)
	got, ok, err := s.HiringTeam(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != (messages.Recipient{Name: "Bruno Tavares", URL: "https://www.linkedin.com/in/bruno-tavares-fictional"}) {
		t.Errorf("HiringTeam = %+v, %v", got, ok)
	}
}

// TestHiringTeamWithoutAMessageButtonOrWithoutAHirer finds no recipient.
func TestHiringTeamWithoutAMessageButtonOrWithoutAHirer(t *testing.T) {
	for name, s := range map[string]*Session{
		"an empty hiring team slot": hiringTeamPage(t, "10_no_hiring_team.html"),
		"a hirer with no Message control": hiringTeamPage(t, "07_hiring_team.html",
			`aria-label="Message"`, `aria-label="Connect"`),
		"a search pane hirer with no Message button": hiringTeamPage(t, "11_search_pane_hiring_team.html",
			`data-test-icon="send-privately-small"`, `data-test-icon="connect-small"`,
			"    Message\n</span>", "    Connect\n</span>"),
	} {
		got, ok, err := s.HiringTeam(context.Background())
		if err != nil || ok || got != (messages.Recipient{}) {
			t.Errorf("%s: HiringTeam = %+v, %v, %v; want no recipient and no error", name, got, ok, err)
		}
	}
}
