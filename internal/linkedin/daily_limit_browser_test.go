//go:build browser

package linkedin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/chrome"
)

// TestTheDailyLimitNoticeUnderADisabledButtonIsSeenInChrome: the notice of
// 2026-10-20 (07) is LinkedIn's daily limit; the captured job page is not.
func TestTheDailyLimitNoticeUnderADisabledButtonIsSeenInChrome(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata/fixtures")))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	br, err := browser.Open(ctx, browser.Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	s := NewSession(br.Main(), br, nil, nil)
	for page, want := range map[string]bool{"07_daily_limit_notice.html": true, "03_job_details_top_card.html": false} {
		if err := s.Page.Navigate(ctx, srv.URL+"/"+page); err != nil {
			t.Fatal(err)
		}
		if got := s.DailyLimitReached(); got != want {
			t.Errorf("%s: daily limit %v, want %v", page, got, want)
		}
	}
}
