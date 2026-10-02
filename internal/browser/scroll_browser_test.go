//go:build browser

package browser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/chrome"
)

// TestScrollIntoViewBringsAFarElementToTheMiddle drives a real headless Chrome:
// with and without smooth scrolling, an element 5000 px down ends up in view.
func TestScrollIntoViewBringsAFarElementToTheMiddle(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html><body style="margin:0"><div style="height:5000px"></div><div id="target" style="height:50px">far</div><div style="height:5000px"></div></body></html>`)
	}))
	defer srv.Close()
	for _, smooth := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		br, err := Open(ctx, Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true, SmoothScroll: smooth})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		pg := br.Main()
		if err := pg.Navigate(ctx, srv.URL); err != nil {
			t.Fatal(err)
		}
		target, err := pg.Element("//div[@id='target']")
		if err != nil {
			t.Fatal(err)
		}
		if err := target.ScrollIntoView(); err != nil {
			t.Fatalf("smooth=%v: %v", smooth, err)
		}
		top, _ := target.(*rodElement).el.Eval(`() => String(window.scrollY)`)
		n, _ := strconv.ParseFloat(top.Value.String(), 64)
		if n < 4000 || n > 5100 {
			t.Errorf("smooth=%v: scrollY %v, want the target near the middle of the viewport", smooth, n)
		}
		_ = br.Close()
		cancel()
	}
}
