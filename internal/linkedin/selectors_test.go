package linkedin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

func fixture(t *testing.T, name string) *html.Node {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := htmlquery.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func count(t *testing.T, root *html.Node, xpath string) int {
	t.Helper()
	nodes, err := htmlquery.QueryAll(root, xpath)
	if err != nil {
		t.Fatalf("bad xpath %q: %v", xpath, err)
	}
	return len(nodes)
}

func one(t *testing.T, root *html.Node, xpath string) *html.Node {
	t.Helper()
	nodes, err := htmlquery.QueryAll(root, xpath)
	if err != nil {
		t.Fatalf("bad xpath %q: %v", xpath, err)
	}
	if len(nodes) != 1 {
		t.Fatalf("%q matched %d nodes, want 1", xpath, len(nodes))
	}
	return nodes[0]
}

func text(n *html.Node) string { return strings.Join(strings.Fields(htmlquery.InnerText(n)), " ") }

// ------------------------------------------------------------ login (05)
func TestLoginDeadLocatorsAreReallyDead(t *testing.T) {
	login := fixture(t, "05_login_page.html")
	for _, dead := range []string{"//input[@id='username']", "//input[@id='password']", "//input[@name='session_key']", "//form", "//button[@type='submit']"} {
		if count(t, login, dead) != 0 {
			t.Errorf("%s came back", dead)
		}
	}
}

func TestLoginFieldLocatorsMatchTheRealPage(t *testing.T) {
	login := fixture(t, "05_login_page.html")
	// 1 real + 1 hidden duplicate each; the browser layer picks the displayed one.
	if count(t, login, "//input[@type='email']") != 2 || count(t, login, "//input[@type='password']") != 2 {
		t.Error("email/password inputs")
	}
	if LoginEmailCSS != "input[type='email']" || LoginPasswordCSS != "input[type='password']" {
		t.Error("constants drifted from the measured locators")
	}
}

func TestSignInButtonNeedsAnExactTextMatch(t *testing.T) {
	login := fixture(t, "05_login_page.html")
	contains := count(t, login, `//button[contains(normalize-space(.), "Sign in")]`)
	exact := count(t, login, SignInButtonXPath)
	if contains != 4 || exact != 2 {
		t.Errorf("contains=%d exact=%d; contains() would click 'Sign in with Apple'", contains, exact)
	}
	if strings.Contains(SignInButtonXPath, "contains") {
		t.Error("the sign-in locator must not use contains()")
	}
	if count(t, login, `//a[normalize-space(.)="`+ForgotPasswordText+`"]`) == 0 {
		t.Error("the forgot-password link that gates the login wait is gone")
	}
}

// ------------------------------------------------------------ job card (01)
func TestJobCardAnchors(t *testing.T) {
	card := fixture(t, "01_job_card.html")
	link := one(t, card, "/"+JobCardTitleLinkXPath)
	aria := htmlquery.SelectAttr(link, "aria-label")
	if strings.TrimSuffix(aria, TitleVerificationSuffix) != "Forward Deployed Engineer" {
		t.Errorf("title aria-label %q", aria)
	}
	if text(one(t, card, "/"+JobCardCompanyXPath)) != "TechNovaTime" {
		t.Error("company subtitle")
	}
	loc := text(one(t, card, "/"+JobCardLocationXPath))
	if loc != "New York, United States (Hybrid)" {
		t.Errorf("location %q", loc)
	}
	if count(t, card, "//li[@data-occludable-job-id]") == 0 || count(t, card, "//*[contains(@class,'"+JobCardFooterStateClass+"')]") == 0 {
		t.Error("stable anchors missing")
	}
	if count(t, card, "/"+JobCardEasyApplyXPath) == 0 {
		t.Error("card-level Easy Apply flag")
	}
	// Randomised class names: nothing may key off them.
	for _, dead := range []string{"job-card-container__primary-description", "job-card-container__metadata-item", "job-card-list__title"} {
		if count(t, card, "//*[contains(concat(' ', normalize-space(@class), ' '), ' "+dead+" ')]") != 0 {
			t.Errorf("dead class %s is back", dead)
		}
	}
}

// ------------------------------------------------------------ apply button (04)
func TestApplyButtonStillSaysEasyApply(t *testing.T) {
	btn := one(t, fixture(t, "04_apply_button.html"), "//button")
	if htmlquery.SelectAttr(btn, "aria-label") != "Easy Apply to Forward Deployed Engineer at TechNovaTime" || text(btn) != "Easy Apply" {
		t.Error("the 'LinkedIn dropped Easy from the aria-label' claim is still false")
	}
	if htmlquery.SelectAttr(btn, "id") != ApplyButtonID {
		t.Error("apply button id")
	}
	doc := fixture(t, "04_apply_button.html")
	for _, loc := range EasyApplyLocators {
		if strings.Contains(loc.XPath, "artdeco-button--3") {
			t.Errorf("%s keys off the size token", loc.How)
		}
	}
	if count(t, doc, "/"+EasyApplyLocators[0].XPath) != 1 || count(t, doc, "/"+EasyApplyLocators[2].XPath) != 1 || count(t, doc, "/"+ApplyButtonXPath) != 1 {
		t.Error("apply locators do not match the captured button")
	}
}

func TestEverySelectorIsValidXPath(t *testing.T) {
	all := []string{SignInButtonXPath, JobCardsXPath, JobCardTitleLinkXPath, JobCardCompanyXPath, JobCardLocationXPath,
		JobCardEasyApplyXPath, ApplyButtonXPath, ModalXPath, ModalCloseXPath, FormQuestionXPath, SelectXPath, SelectTitleXPath,
		RadioFieldsetXPath, RadioTitleXPath, RadioInputsXPath, TextInputXPath, TextareaXPath, CheckboxXPath, LabelForXPath,
		HiddenTitleXPath, ResumeUploadInputXPath, TodayButtonXPath, NextButtonXPath, ReviewButtonXPath, SubmitButtonXPath,
		DiscardButtonXPath, SearchLocationInputXPath, AllFiltersButtonXPath, ShowResultsButtonXPath, AddCompanyInputXPath,
		FilterSwitchXPath, PostedAgoXPath, InlineFeedbackXPath, InlineErrorXPath, FormProgressXPath, AppliedOnPageXPath, PaginationActiveXPath,
		OptionLabelXPath("x"), PageButtonXPath(2), ClassXPath("a"), SelectOptionsXPath, VisuallyHiddenAnyXPath,
		FollowCompanyInputXPath, FollowCompanyLabelXPath, FromDocument(ModalCloseXPath)}
	for _, loc := range EasyApplyLocators {
		all = append(all, loc.XPath)
	}
	doc := fixture(t, "04_apply_button.html")
	for _, xp := range all {
		if _, err := htmlquery.QueryAll(doc, strings.TrimPrefix(xp, ".")); err != nil {
			t.Errorf("%s: %v", xp, err)
		}
	}
}

func TestEasyApplyLocatorOrder(t *testing.T) {
	if !strings.Contains(EasyApplyLocators[0].XPath, ApplyButtonID) {
		t.Error("the stable id must be tried first")
	}
	found := false
	for _, loc := range EasyApplyLocators {
		found = found || strings.Contains(loc.XPath, EasyApplyAriaLabelPrefix)
	}
	if !found || EasyApplyLocators[len(EasyApplyLocators)-1].XPath != ApplyButtonXPath {
		t.Error("an Easy Apply text locator and the generic apply button must close the list")
	}
}

// ------------------------------------------------------------ modal (06)
func TestModalAndFormAnchors(t *testing.T) {
	modal := fixture(t, "06_easy_apply_modal.html")
	dialog := one(t, modal, ModalXPath)
	if count(t, dialog, ModalCloseXPath) == 0 {
		t.Error("close button")
	}
	if n := count(t, dialog, FormQuestionXPath); n != 3 {
		t.Errorf("form elements %d, want 3", n)
	}
	if count(t, dialog, SelectXPath) != 2 || count(t, dialog, TextInputXPath) != 1 || count(t, dialog, TextareaXPath) != 0 {
		t.Error("control counts")
	}
	questions, _ := htmlquery.QueryAll(dialog, FormQuestionXPath)
	for _, q := range questions {
		if count(t, q, SelectXPath) == 1 && count(t, q, SelectTitleXPath) == 0 {
			t.Error("a select question without a readable title")
		}
		if count(t, q, TextInputXPath) == 1 && count(t, q, LabelForXPath) == 0 {
			t.Error("a text question without a label[@for]")
		}
	}
	if count(t, dialog, ResumeUploadInputXPath) != 1 || count(t, dialog, FollowCompanyInputXPath) != 1 || count(t, dialog, FollowCompanyLabelXPath) != 1 {
		t.Error("resume input / follow checkbox and its label")
	}
	if bar := one(t, dialog, FormProgressXPath); !strings.Contains(htmlquery.SelectAttr(bar, "aria-label"), "progress is at 0 percent") {
		t.Error("the progress region's label")
	}
	if count(t, dialog, InlineErrorXPath) != 0 { // a form LinkedIn has not refused: no question flagged
		t.Error("an inline error matched in a form with none")
	}
	submit := one(t, dialog, SubmitButtonXPath)
	if htmlquery.SelectAttr(submit, "type") != "button" {
		t.Error("submit is type=button, not submit")
	}
	// The captured application was single page: Next/Review must not match inside the dialog...
	if count(t, dialog, NextButtonXPath) != 0 || count(t, dialog, ReviewButtonXPath) != 0 {
		t.Error("Next/Review matched inside a single-page modal")
	}
	// ...and every modal button locator is relative, never document-wide.
	for _, xp := range []string{NextButtonXPath, ReviewButtonXPath, SubmitButtonXPath, DiscardButtonXPath, ModalCloseXPath, FormQuestionXPath} {
		if !strings.HasPrefix(xp, ".//") {
			t.Errorf("%s is not scoped to the dialog", xp)
		}
	}
	if count(t, modal, "//*[contains(@class,'jobs-easy-apply-form-element')]") != 0 {
		t.Error("the dead form class came back")
	}
}

// ------------------------------------------------------------ after Submit
func TestTheDoneButtonIsOnlyLookedForInADialog(t *testing.T) {
	doc, err := htmlquery.Parse(strings.NewReader(`<ul><li><span>Software Delivery Done Right</span></li></ul>
<div role="dialog"><h2>Application sent</h2><button aria-label="Dismiss" data-test-modal-close-btn>x</button><button><span>Done</span></button></div>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := text(one(t, doc, DialogButtonXPath(DoneButtonText))); got != "Done" {
		t.Errorf("matched %q", got)
	}
	if count(t, doc, FromDocument(ModalCloseXPath)) != 1 {
		t.Error("the dialog's close button is not found")
	}
}
