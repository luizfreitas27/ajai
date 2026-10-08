package linkedin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/pacing"
)

// urlPage is a Page whose URL follows a script: each URL() call returns the next entry.
type urlPage struct {
	browser.Page
	urls []string
	i    int
}

func (p *urlPage) Navigate(context.Context, string) error { return nil }
func (p *urlPage) URL() (string, error) {
	if p.i < len(p.urls)-1 {
		p.i++
	}
	return p.urls[p.i], nil
}

func TestLoginIsDetectedOnlyByTheFeedURLAndWaitsForTheUser(t *testing.T) {
	s := NewSession(&urlPage{urls: []string{"https://www.linkedin.com/feed/?trk=x"}}, nil, pacing.New(config.Defaults().Pacing, true), nil)
	waits := 0
	if err := s.EnsureLoggedIn(context.Background(), time.Minute, func() { waits++ }); err != nil || waits != 0 {
		t.Errorf("%v %d", err, waits)
	}
	// Signed out: LinkedIn sends the feed request to /login (or an authwall) in any language.
	for _, signedOut := range []string{"https://www.linkedin.com/login?fromSignIn=true", "https://www.linkedin.com/authwall?trk=x", "https://br.linkedin.com/checkpoint/lg/login"} {
		s = NewSession(&urlPage{urls: []string{signedOut}}, nil, pacing.New(config.Defaults().Pacing, true), nil)
		if s.IsLoggedIn() {
			t.Errorf("%s taken as logged in", signedOut)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = NewSession(&urlPage{urls: []string{"https://www.linkedin.com/login"}}, nil, pacing.New(config.Defaults().Pacing, true), nil)
	err := s.EnsureLoggedIn(ctx, time.Minute, func() { waits++ })
	if !errors.Is(err, context.Canceled) || waits != 1 {
		t.Errorf("%v %d", err, waits)
	}
}

func TestSplitLocation(t *testing.T) {
	cases := map[string][2]string{
		"New York, United States (Hybrid)": {"New York, United States", "Hybrid"},
		"São Paulo, Brazil (Remote)":       {"São Paulo, Brazil", "Remote"},
		"Lisbon":                           {"Lisbon", "Unknown"},
		"":                                 {"Unknown", "Unknown"},
	}
	for in, want := range cases {
		loc, style := SplitLocation(in)
		if loc != want[0] || style != want[1] {
			t.Errorf("%q -> %q %q", in, loc, style)
		}
	}
}

func TestAPostingIsTheTitleCompanyAndLocationOrOnlyTheJob(t *testing.T) {
	role := Card{ID: "1", Title: "Java Specialist", Company: "Globex", Location: "Brazil"}
	if again := (Card{ID: "2", Title: " java  SPECIALIST", Company: "globex", Location: "Brazil "}); again.Posting() != role.Posting() {
		t.Errorf("the same role posted again: %q, want %q", again.Posting(), role.Posting())
	}
	for name, c := range map[string]Card{
		"another city":    {ID: "2", Title: "Java Specialist", Company: "Globex", Location: "Lisbon"},
		"another company": {ID: "2", Title: "Java Specialist", Company: "Initech", Location: "Brazil"},
	} {
		if c.Posting() == role.Posting() {
			t.Errorf("%s: %q is the same role", name, c.Posting())
		}
	}
	for name, c := range map[string]Card{ // a card missing a part names only its own job
		"an unreadable title":   {ID: "2", Company: "Globex", Location: "Brazil"},
		"an unreadable company": {ID: "2", Title: "Java Specialist", Location: "Brazil"},
		"an unknown location":   {ID: "2", Title: "Java Specialist", Company: "Globex", Location: "Unknown"},
	} {
		twin := c
		twin.ID = "3"
		if c.Posting() == twin.Posting() || c.Posting() == role.Posting() {
			t.Errorf("%s: %q passes for another job", name, c.Posting())
		}
	}
}

func TestParsePosted(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	posted, reposted, ok := ParsePosted("Reposted 3 days ago", now)
	if !ok || !reposted || posted != now.Add(-72*time.Hour) {
		t.Errorf("%v %v %v", posted, reposted, ok)
	}
	if _, _, ok := ParsePosted("Promoted", now); ok {
		t.Error("no phrase")
	}
	if p, _, _ := ParsePosted("2 weeks ago", now); p != now.Add(-14*24*time.Hour) {
		t.Errorf("%v", p)
	}
}
