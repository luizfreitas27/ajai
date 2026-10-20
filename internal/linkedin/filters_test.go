package linkedin

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/pacing"
)

// fakeElement records clicks and typed text. The embedded interface value is nil:
// any method not overridden here would panic, which is what a test wants.
type fakeElement struct {
	stub    browser.Element
	name    string
	page    *fakePage
	typed   strings.Builder
	checked string // the "checked" property: "true" for a switch already on
	aria    string // the aria-checked attribute
	stuck   bool   // a switch the click leaves off
}

func (e *fakeElement) Property(name string) (string, error) {
	if name == "checked" {
		return e.checked, nil
	}
	return e.stub.Property("")
}
func (e *fakeElement) Focus() error                               { return e.stub.Focus() }
func (e *fakeElement) Elements(string) ([]browser.Element, error) { return e.stub.Elements("") }
func (e *fakeElement) SelectOption(string) error                  { return e.stub.SelectOption("") }
func (e *fakeElement) SetFiles([]string) error                    { return e.stub.SetFiles(nil) }
func (e *fakeElement) ShadowElements(string) ([]browser.Element, error) {
	return e.stub.ShadowElements("")
}
func (e *fakeElement) ScriptClick() error { return e.stub.ScriptClick() }

func (e *fakeElement) Click() error { e.page.clicks = append(e.page.clicks, e.name); return nil }
func (e *fakeElement) ClickAtCentre() error {
	e.page.clicks = append(e.page.clicks, e.name+" (at centre)")
	if !e.stuck {
		e.checked = "true"
	}
	return nil
}
func (e *fakeElement) Clear() error            { e.typed.Reset(); return nil }
func (e *fakeElement) Input(text string) error { e.typed.WriteString(text); return nil }
func (e *fakeElement) ScrollIntoView() error   { return nil }
func (e *fakeElement) Visible() bool           { return true }
func (e *fakeElement) Text() (string, error)   { return e.name, nil }
func (e *fakeElement) Attr(name string) (string, bool) {
	if name == "aria-checked" && e.aria != "" {
		return e.aria, true
	}
	return "", false
}
func (e *fakeElement) Element(xpath string) (browser.Element, error) {
	return e.page.Element(xpath)
}

// fakePage maps XPath substrings to elements; everything else is "not found".
type fakePage struct {
	browser.Page
	elements map[string]*fakeElement // key: a substring of the xpath
	clicks   []string
	keys     []string
}

func (p *fakePage) Navigate(context.Context, string) error { return nil }
func (p *fakePage) URL() (string, error)                   { return FeedURL, nil }
func (p *fakePage) Press(key string) error                 { p.keys = append(p.keys, key); return nil }
func (p *fakePage) Element(xpath string) (browser.Element, error) {
	for key, el := range p.elements {
		if strings.Contains(xpath, key) {
			return el, nil
		}
	}
	return nil, &browser.ErrNotFound{XPath: xpath}
}
func (p *fakePage) WaitElement(_ context.Context, xpath string, _ time.Duration) (browser.Element, error) {
	return p.Element(xpath)
}

// quick is a pacer with no pauses, for tests.
func quick() *pacing.Pacer {
	cfg := config.Defaults().Pacing
	cfg.BetweenActionsMin, cfg.BetweenActionsMax, cfg.TypingDelayMinMs, cfg.TypingDelayMaxMs = 0, 0, 0, 0
	return pacing.New(cfg, true)
}

func newFakePage(names ...string) *fakePage {
	p := &fakePage{elements: map[string]*fakeElement{}}
	for _, n := range names {
		p.elements[n] = &fakeElement{name: n, page: p}
	}
	return p
}

func TestApplyFiltersClicksEveryConfiguredControlInThePythonOrder(t *testing.T) {
	// Keys are substrings of the XPaths the session builds: text matches are lowercased
	// by TextXPath, the company search box is matched by its placeholder attribute.
	page := newFakePage("City, state, or zip code", "All filters", `"most recent"`, `"past week"`, `"mid-senior level"`, `"remote"`,
		`"acme"`, `"add a company"`, `placeholder='Add a company'`, `"easy apply"`, "apply current filters to show")
	// The switch: an h3 "Easy Apply" inside a fieldset whose input[@role=switch] is the element clicked.
	page.elements[`role="switch"`] = &fakeElement{name: "switch", page: page}
	s := NewSession(page, nil, quick(), nil)
	f := config.Defaults().Search
	f.SortBy, f.DatePosted, f.ExperienceLevel, f.OnSite = "Most recent", "Past week", []string{"Mid-Senior level"}, []string{"Remote"}
	f.Companies = []string{"ACME", "Globex"} // Globex is not in the list: typed into "Add a company"
	if err := s.ApplyFilters(context.Background(), f, "Lisbon"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(page.clicks, " > ")
	want := "City, state, or zip code > All filters > \"most recent\" > \"past week\" > \"mid-senior level\" > \"acme\" > \"add a company\" > placeholder='Add a company' > \"remote\" > switch (at centre) > apply current filters to show"
	if got != want {
		t.Errorf("clicks:\n got %s\nwant %s", got, want)
	}
	if typed := page.elements["City, state, or zip code"].typed.String(); typed != "Lisbon" {
		t.Errorf("location typed %q", typed)
	}
	if typed := page.elements[`placeholder='Add a company'`].typed.String(); typed != "Globex" {
		t.Errorf("company typed %q", typed)
	}
	if strings.Join(page.keys, ",") != "Enter,ArrowDown,Enter" {
		t.Errorf("keys %v", page.keys)
	}
}

func TestApplyFiltersFailsOnlyWhenThePanelItselfIsMissing(t *testing.T) {
	s := NewSession(newFakePage("apply current filters to show"), nil, quick(), nil)
	if err := s.ApplyFilters(context.Background(), config.Defaults().Search, ""); err == nil || !strings.Contains(err.Error(), "All filters") {
		t.Errorf("%v", err)
	}
	page := newFakePage("All filters", "apply current filters to show")
	s = NewSession(page, nil, quick(), nil)
	f := config.Defaults().Search
	f.DatePosted = "Past week" // not on the page: logged and skipped, the search still runs
	if err := s.ApplyFilters(context.Background(), f, ""); err != nil {
		t.Errorf("%v", err)
	}
}

func TestXPathLiteralHandlesQuotes(t *testing.T) {
	cases := map[string]string{
		`plain`:       `"plain"`,
		`it's`:        `"it's"`,
		`say "hi"`:    `'say "hi"'`,
		`it's "both"`: `concat("it's ", '"', "both", '"')`,
	}
	for in, want := range cases {
		if got := XPathLiteral(in); got != want {
			t.Errorf("XPathLiteral(%q) = %s, want %s", in, got, want)
		}
	}
	if !strings.Contains(TextXPath("span", `Say "Hi"`), `'say "hi"'`) {
		t.Error(TextXPath("span", `Say "Hi"`))
	}
}

// easyApplyOnly sets the filters with only the Easy Apply switch on, sw, and
// returns what was logged.
func easyApplyOnly(t *testing.T, sw *fakeElement) string {
	t.Helper()
	page := newFakePage("All filters", `"easy apply"`, "apply current filters to show")
	sw.name, sw.page = "switch", page
	page.elements[`role="switch"`] = sw
	var log bytes.Buffer
	s := NewSession(page, nil, quick(), slog.New(slog.NewTextHandler(&log, nil)))
	f := config.Defaults().Search
	f.EasyApplyOnly = true
	if err := s.ApplyFilters(context.Background(), f, ""); err != nil {
		t.Fatal(err)
	}
	return log.String()
}

func TestASwitchAlreadyOnIsLeftOn(t *testing.T) {
	for name, sw := range map[string]*fakeElement{"checked": {checked: "true"}, "aria-checked": {aria: "true"}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel() // each run waits for the results to settle
			easyApplyOnly(t, sw)
			for _, c := range sw.page.clicks {
				if strings.HasPrefix(c, "switch") {
					t.Errorf("a switch already on was clicked (off): %q", sw.page.clicks)
				}
			}
		})
	}
}

func TestASwitchTheClickLeavesOffIsLogged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stuck bool
		want  string // the switch's warning, "" for none
	}{
		{"turned on", false, ""},
		{"left off", true, `level=WARN msg="filter switch did not turn on" title="Easy Apply"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := easyApplyOnly(t, &fakeElement{stuck: tc.stuck})
			if tc.want == "" && strings.Contains(log, "filter switch") || !strings.Contains(log, tc.want) {
				t.Errorf("want %q, logged:\n%s", tc.want, log)
			}
		})
	}
}
