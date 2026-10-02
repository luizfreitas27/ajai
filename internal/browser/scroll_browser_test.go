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

// scrollPage has a target 5000 px down the page and a button 3000 px down a
// scrollable list (LinkedIn's job list scrolls inside a container). It counts
// scroll events: a smooth scroll fires many, an instant one fires one.
const scrollPage = `<html><body style="margin:0">
<div style="height:5000px"></div><div id="target" style="height:50px">far</div><div style="height:5000px"></div>
<div id="list" style="position:fixed;top:0;right:0;width:200px;height:300px;overflow:auto">
  <div style="height:3000px"></div><button id="inner" onclick="window.clicked=true">apply</button><div style="height:3000px"></div>
</div>
<script>
window.pageScrolls = 0; window.listScrolls = 0; window.clicked = false;
window.addEventListener("scroll", () => window.pageScrolls++);
document.getElementById("list").addEventListener("scroll", () => window.listScrolls++);
</script></body></html>`

// TestScrollIntoViewAndClickScrollSmoothlyOnlyWhenAsked drives a real headless
// Chrome: with and without smooth scrolling, an element 5000 px down ends up in
// the middle of the viewport, a button deep in a scrollable list is clicked, and
// only smooth mode scrolls in steps.
func TestScrollIntoViewAndClickScrollSmoothlyOnlyWhenAsked(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, scrollPage)
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
		number := func(js string) float64 {
			v, err := pg.(*rodPage).p.Eval(`() => String(` + js + `)`)
			if err != nil {
				t.Fatal(err)
			}
			n, _ := strconv.ParseFloat(v.Value.String(), 64)
			return n
		}

		target, err := pg.Element("//div[@id='target']")
		if err != nil {
			t.Fatal(err)
		}
		if err := target.ScrollIntoView(); err != nil {
			t.Fatalf("smooth=%v: %v", smooth, err)
		}
		if y := number("window.scrollY"); y < 4000 || y > 5100 {
			t.Errorf("smooth=%v: scrollY %v, want the target near the middle of the viewport", smooth, y)
		}
		if n := number("window.pageScrolls"); smooth != (n > 2) {
			t.Errorf("smooth=%v: %v scroll events", smooth, n)
		}

		inner, err := pg.Element("//button[@id='inner']")
		if err != nil {
			t.Fatal(err)
		}
		if err := inner.Click(); err != nil {
			t.Fatalf("smooth=%v: click: %v", smooth, err)
		}
		if number("window.clicked ? 1 : 0") != 1 {
			t.Errorf("smooth=%v: the click inside the scrolled list did not land", smooth)
		}
		if n := number("window.listScrolls"); smooth != (n > 2) {
			t.Errorf("smooth=%v: %v scroll events in the list", smooth, n)
		}
		_ = br.Close()
		cancel()
	}
}
