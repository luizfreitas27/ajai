//go:build browser

package browser

import (
	"context"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/chrome"
)

// TestHeadlessWindowOpensAndNavigates opens Chrome with settings.window set to
// headless (--headless=new) on a throwaway profile, loads a data: page and reads
// its title back. QuitOnClose quits Chrome on Close; the profile is a TempDir.
func TestHeadlessWindowOpensAndNavigates(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	br, err := Open(ctx, Options{Bin: bin, ProfileDir: t.TempDir(), Window: WindowHeadless, QuitOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	const page = "data:text/html,<title>ajai headless</title><p>ok</p>"
	pg := br.Main()
	if err := pg.Navigate(ctx, page); err != nil {
		t.Fatal(err)
	}
	title, err := pg.(*rodPage).p.Eval(`() => document.title`)
	if err != nil {
		t.Fatal(err)
	}
	if got := title.Value.String(); got != "ajai headless" {
		t.Errorf("title = %q, want %q", got, "ajai headless")
	}
}
