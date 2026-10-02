package linkedin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/pacing"
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
	s := NewSession(&urlPage{urls: []string{"https://www.linkedin.com/feed/?trk=x"}}, pacing.New(config.Defaults().Pacing, true), nil)
	waits := 0
	if err := s.EnsureLoggedIn(context.Background(), time.Minute, func() { waits++ }); err != nil || waits != 0 {
		t.Errorf("%v %d", err, waits)
	}
	// Signed out: LinkedIn sends the feed request to /login (or an authwall) in any language.
	for _, signedOut := range []string{"https://www.linkedin.com/login?fromSignIn=true", "https://www.linkedin.com/authwall?trk=x", "https://br.linkedin.com/checkpoint/lg/login"} {
		s = NewSession(&urlPage{urls: []string{signedOut}}, pacing.New(config.Defaults().Pacing, true), nil)
		if s.IsLoggedIn() {
			t.Errorf("%s taken as logged in", signedOut)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = NewSession(&urlPage{urls: []string{"https://www.linkedin.com/login"}}, pacing.New(config.Defaults().Pacing, true), nil)
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
