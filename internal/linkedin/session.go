package linkedin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/pacing"
)

// Session drives LinkedIn in one tab: login state, search, filters, job cards,
// job details. It knows the markup (selectors.go) and nothing about what to do
// with a job; the run loop decides that.
type Session struct {
	Page   browser.Page
	Pacer  *pacing.Pacer
	Logger *slog.Logger
}

// NewSession wires a session; logger may be nil.
func NewSession(page browser.Page, pacer *pacing.Pacer, logger *slog.Logger) *Session {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Session{Page: page, Pacer: pacer, Logger: logger}
}

// URLs.
const (
	LoginURL  = "https://www.linkedin.com/login"
	FeedURL   = "https://www.linkedin.com/feed"
	SearchURL = "https://www.linkedin.com/jobs/search/?keywords="
	JobURL    = "https://www.linkedin.com/jobs/view/"
)

// IsLoggedIn is true only when the tab is on the feed. LinkedIn sends a
// signed-out visitor from the feed to /login or an authwall, in whatever
// language it picked for the visitor, so the URL is the one reliable,
// language-independent signal. (The Python bot assumed "logged in" when it saw
// no Sign in link, which on a Portuguese page is always.)
func (s *Session) IsLoggedIn() bool {
	u, err := s.Page.URL()
	return err == nil && strings.HasPrefix(u, FeedURL)
}

// ErrLoginTimeout: the user did not sign in within the allowed time.
var ErrLoginTimeout = errors.New("no LinkedIn login detected in time")

// EnsureLoggedIn opens the feed and, when LinkedIn redirects to a sign-in page,
// waits for the user to sign in by hand (aja never types a password). onWait is
// called once when waiting starts, so the caller can tell the user.
func (s *Session) EnsureLoggedIn(ctx context.Context, timeout time.Duration, onWait func()) error {
	if err := s.Page.Navigate(ctx, FeedURL); err != nil {
		return err
	}
	if s.IsLoggedIn() {
		return nil
	}
	if onWait != nil {
		onWait()
	}
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		if s.IsLoggedIn() {
			s.Logger.Info("linkedin login detected")
			return nil
		}
		if time.Now().After(deadline) {
			return ErrLoginTimeout
		}
	}
}

// Search opens the job search for a term.
func (s *Session) Search(ctx context.Context, term string) error {
	return s.Page.Navigate(ctx, SearchURL+url.QueryEscape(term))
}

// TypeHuman types text one character at a time with the pacer's delays.
func (s *Session) TypeHuman(ctx context.Context, el browser.Element, text string) error {
	for _, r := range text {
		if err := el.Input(string(r)); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Pacer.TypingDelay()):
		}
	}
	return nil
}

// spanXPath matches a span whose text contains text, ignoring ASCII case.
func spanXPath(tag, text string) string {
	return fmt.Sprintf(`.//%s[contains(translate(normalize-space(.), "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz"), %q)]`,
		tag, strings.ToLower(strings.TrimSpace(text)))
}

// clickText clicks the first visible span carrying text; false when none.
func (s *Session) clickText(ctx context.Context, text string, wait time.Duration) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	el, err := s.Page.WaitElement(ctx, spanXPath("span", text), wait)
	if err != nil {
		s.Logger.Warn("filter control not found", "text", text)
		return false
	}
	if err := el.Click(); err != nil {
		s.Logger.Warn("filter click failed", "text", text, "error", err)
		return false
	}
	_ = s.Pacer.BetweenActions(ctx)
	return true
}

func (s *Session) clickSwitch(ctx context.Context, title string) {
	container, err := s.Page.Element(spanXPath("h3", title) + "/ancestor::fieldset")
	if err != nil {
		s.Logger.Warn("filter switch not found", "title", title)
		return
	}
	sw, err := container.Element(`.//input[@role="switch"]`)
	if err != nil {
		s.Logger.Warn("filter switch input not found", "title", title)
		return
	}
	if err := sw.Click(); err != nil {
		s.Logger.Warn("filter switch click failed", "title", title, "error", err)
	}
	_ = s.Pacer.BetweenActions(ctx)
}

// ApplyFilters sets the search location and every configured filter through
// LinkedIn's "All filters" panel, as the Python apply_filters did. Each control
// that cannot be found is logged and skipped; the search still runs.
func (s *Session) ApplyFilters(ctx context.Context, f config.Search, location string) error {
	if strings.TrimSpace(location) != "" {
		if el, err := s.Page.Element(`.//input[@aria-label='City, state, or zip code' and not(@disabled)]`); err == nil {
			_ = el.Click()
			_ = el.Clear()
			if err := s.TypeHuman(ctx, el, strings.TrimSpace(location)); err != nil {
				return err
			}
			_ = s.Pacer.BetweenActions(ctx)
			_ = s.Page.Press("Enter")
		} else {
			s.Logger.Warn("search location box not found; continuing with LinkedIn's default")
		}
	}
	all, err := s.Page.WaitElement(ctx, `//button[normalize-space()="All filters"]`, 10*time.Second)
	if err != nil {
		return fmt.Errorf("the All filters button did not appear: %w", err)
	}
	if err := all.Click(); err != nil {
		return err
	}
	_ = s.Pacer.BetweenActions(ctx)

	short := 2 * time.Second
	s.clickText(ctx, f.SortBy, short)
	s.clickText(ctx, f.DatePosted, short)
	for _, group := range [][]string{f.ExperienceLevel, f.Companies, f.JobType, f.OnSite, f.Locations, f.Industry, f.JobFunction, f.JobTitles} {
		for _, text := range group {
			s.clickText(ctx, text, short)
		}
	}
	if f.EasyApplyOnly {
		s.clickSwitch(ctx, "Easy Apply")
	}
	if f.Under10Applicants {
		s.clickSwitch(ctx, "Under 10 applicants")
	}
	if f.InYourNetwork {
		s.clickSwitch(ctx, "In your network")
	}
	if f.FairChanceEmployer {
		s.clickSwitch(ctx, "Fair Chance Employer")
	}
	s.clickText(ctx, f.Salary, short)
	for _, text := range append(append([]string{}, f.Benefits...), f.Commitments...) {
		s.clickText(ctx, text, short)
	}
	show, err := s.Page.WaitElement(ctx, `//button[contains(translate(@aria-label, "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz"), "apply current filters to show")]`, 10*time.Second)
	if err != nil {
		return fmt.Errorf("the Show results button did not appear: %w", err)
	}
	if err := show.Click(); err != nil {
		return err
	}
	select { // let the results reload settle before anything reads the list
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	return nil
}

// Card is what a job card in the results list says.
type Card struct {
	ID             string
	Title          string
	Company        string
	Location       string
	Style          string // Remote, Hybrid, On-site, Unknown
	AlreadyApplied bool
	Link           string
}

// WaitForCards waits for the results list to render.
func (s *Session) WaitForCards(ctx context.Context) error {
	_, err := s.Page.WaitElement(ctx, JobCardsXPath, 15*time.Second)
	return err
}

// Cards re-queries the virtualised results list; call it before every index.
func (s *Session) Cards() ([]browser.Element, error) { return s.Page.Elements(JobCardsXPath) }

// ReadCard extracts the card's details (without opening it).
func (s *Session) ReadCard(card browser.Element) (Card, error) {
	var c Card
	c.ID, _ = card.Attr(JobIDAttribute)
	link, err := card.Element(JobCardTitleLinkXPath)
	if err != nil {
		return c, fmt.Errorf("card %s has no title link: %w", c.ID, err)
	}
	title, ok := link.Attr("aria-label")
	if !ok || title == "" {
		title, _ = link.Text()
	}
	c.Title = strings.TrimSpace(strings.TrimSuffix(strings.SplitN(title, "\n", 2)[0], TitleVerificationSuffix))
	if company, err := card.Element(JobCardCompanyXPath); err == nil {
		t, _ := company.Text()
		c.Company = strings.TrimSpace(t)
	}
	c.Location, c.Style = "Unknown", "Unknown"
	if loc, err := card.Element(JobCardLocationXPath); err == nil {
		t, _ := loc.Text()
		c.Location, c.Style = SplitLocation(strings.TrimSpace(t))
	}
	if state, err := card.Element(".//*[contains(@class,'" + JobCardFooterStateClass + "')]"); err == nil {
		t, _ := state.Text()
		c.AlreadyApplied = strings.TrimSpace(t) == "Applied"
	}
	c.Link = JobURL + c.ID
	return c, nil
}

// SplitLocation turns "New York, United States (Hybrid)" into its two parts.
func SplitLocation(text string) (location, style string) {
	if i := strings.LastIndex(text, "("); i >= 0 && strings.HasSuffix(text, ")") {
		return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1 : len(text)-1])
	}
	if text == "" {
		return "Unknown", "Unknown"
	}
	return text, "Unknown"
}

// OpenCard clicks the card's title so the details pane shows this job.
func (s *Session) OpenCard(ctx context.Context, card browser.Element) error {
	link, err := card.Element(JobCardTitleLinkXPath)
	if err != nil {
		return err
	}
	if err := link.Click(); err != nil {
		return err
	}
	return s.Pacer.BetweenActions(ctx)
}

// Description returns the job description text, ok false when it cannot be read.
func (s *Session) Description(ctx context.Context) (string, bool) {
	el, err := s.Page.WaitElement(ctx, "//*[contains(@class,'"+JobDescriptionClass+"')]", 5*time.Second)
	if err != nil {
		return "", false
	}
	t, err := el.Text()
	return t, err == nil
}

// AboutCompany returns the "About the company" text, "" when absent.
func (s *Session) AboutCompany() string {
	el, err := s.Page.Element("//*[contains(@class,'" + AboutCompanyClass + "')]")
	if err != nil {
		return ""
	}
	t, _ := el.Text()
	return t
}

var postedRe = regexp.MustCompile(`(?i)(\d+)\s+(second|minute|hour|day|week|month|year)s?\s+ago`)

// DatePosted parses the top card's "Reposted 3 days ago" into an approximate
// time and whether the job was reposted. ok false when nothing was found.
func (s *Session) DatePosted(now time.Time) (posted time.Time, reposted, ok bool) {
	el, err := s.Page.Element(`//*[contains(@class,'jobs-unified-top-card') or contains(@class,'job-details-jobs-unified-top-card')]//span[contains(normalize-space(), " ago")]`)
	if err != nil {
		return time.Time{}, false, false
	}
	text, _ := el.Text()
	return ParsePosted(text, now)
}

// ParsePosted is the pure half of DatePosted.
func ParsePosted(text string, now time.Time) (posted time.Time, reposted, ok bool) {
	reposted = strings.Contains(text, "Reposted")
	m := postedRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, reposted, false
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"second": time.Second, "minute": time.Minute, "hour": time.Hour,
		"day": 24 * time.Hour, "week": 7 * 24 * time.Hour, "month": 30 * 24 * time.Hour, "year": 365 * 24 * time.Hour}[strings.ToLower(m[2])]
	return now.Add(-time.Duration(n) * unit), reposted, true
}

// FindApplyButton reports whether an apply button is on the page and which
// locator found it, without clicking.
func (s *Session) FindApplyButton() (browser.Element, string, bool) {
	for _, loc := range EasyApplyLocators {
		if el, err := s.Page.Element(strings.TrimPrefix(loc.XPath, ".")); err == nil {
			return el, loc.How, true
		}
	}
	return nil, "", false
}

// DailyLimitReached reads LinkedIn's inline notice about the Easy Apply cap.
func (s *Session) DailyLimitReached() bool {
	el, err := s.Page.Element("//*[contains(@class,'artdeco-inline-feedback__message')]")
	if err != nil {
		return false
	}
	t, _ := el.Text()
	return strings.Contains(t, "exceeded the daily application limit")
}

// NextPage moves to the following results page; false at the last page.
func (s *Session) NextPage(ctx context.Context) (bool, error) {
	var pagination browser.Element
	for _, cls := range []string{"jobs-search-pagination__pages", "artdeco-pagination__pages", "artdeco-pagination"} {
		if el, err := s.Page.Element("//*[contains(@class,'" + cls + "')]"); err == nil {
			pagination = el
			break
		}
	}
	if pagination == nil {
		return false, nil
	}
	active, err := pagination.Element(".//button[contains(@class, 'active')]")
	if err != nil {
		return false, nil
	}
	text, _ := active.Text()
	current, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return false, nil
	}
	next, err := pagination.Element(fmt.Sprintf(".//button[@aria-label='Page %d']", current+1))
	if err != nil {
		return false, nil
	}
	if err := next.Click(); err != nil {
		return false, err
	}
	s.Logger.Info("results page", "page", current+1)
	return true, s.Pacer.BetweenActions(ctx)
}
