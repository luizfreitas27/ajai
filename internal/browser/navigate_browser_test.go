//go:build browser

package browser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/chrome"
)

// TestNavigateFollowsAClientSideRedirect drives a real headless Chrome through
// what LinkedIn's job search does: the page it opens replaces itself with another
// before its own load event, so the wait for "load" is cut by a navigation.
// Navigate must end on the page the redirect leads to, not fail with CDP's
// "Inspected target navigated or closed".
func TestNavigateFollowsAClientSideRedirect(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	mux := http.NewServeMux()
	// The first page loads a slow resource, so its load event is still pending
	// when the script replaces the page.
	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html><body>
<script>setTimeout(function () { location.replace("/final") }, 100)</script>
<img src="/slow.png"></body></html>`)
	})
	mux.HandleFunc("/slow.png", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html><body><p id="done">final</p></body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	br, err := Open(ctx, Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Close() })
	pg := br.Main()

	if err := pg.Navigate(ctx, srv.URL+"/start"); err != nil {
		t.Fatalf("Navigate failed on a redirecting page: %v", err)
	}
	got, err := pg.URL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "/final") {
		t.Fatalf("Navigate returned on %q, want the page the redirect leads to (/final)", got)
	}
}
