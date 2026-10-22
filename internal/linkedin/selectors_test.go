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
		FilterSwitchXPath, PostedAgoXPath, DailyLimitXPath, InlineErrorXPath, FormProgressXPath, AppliedOnPageXPath, PaginationActiveXPath,
		OptionLabelXPath("x"), OptionWrapLabelXPath("x"), PageButtonXPath(2), ClassXPath("a"), SelectOptionsXPath, VisuallyHiddenAnyXPath,
		FollowCompanyInputXPath, FollowCompanyLabelXPath, FromDocument(ModalCloseXPath),
		MessageOutletXPath, ComposeBubbleXPath, ComposeRecipientXPath, MessageBoxXPath, MessageSendButtonXPath,
		MessageSubjectXPath, InMailCreditsXPath, BubbleCloseButtonXPath, UpsellModalXPath, UpsellDismissXPath, SentMessageBodyXPath}
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

// TestTheDailyLimitNoticeIsFoundInEitherWording: the old inline notice and the one
// seen on 2026-10-20 under a disabled Easy Apply button (12, synthetic), in any case,
// and never on the captured job pages.
func TestTheDailyLimitNoticeIsFoundInEitherWording(t *testing.T) {
	if n := count(t, fixture(t, "12_daily_limit_notice.html"), DailyLimitXPath); n != 1 {
		t.Errorf("the 2026-10-20 notice matched %d times, want 1", n)
	}
	old, err := htmlquery.Parse(strings.NewReader(`<div class="artdeco-inline-feedback__message">You've Exceeded the Daily Application Limit</div>`))
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, old, DailyLimitXPath); n != 1 {
		t.Errorf("the old notice matched %d times, want 1", n)
	}
	for _, page := range []string{"01_job_card.html", "03_job_details_top_card.html", "04_apply_button.html", "06_easy_apply_modal.html"} {
		if n := count(t, fixture(t, page), DailyLimitXPath); n != 0 {
			t.Errorf("%s: matched %d times on a page without the notice", page, n)
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

// ------------------------------------------------------------ hiring team (07, 10, 11)
func TestHiringTeamSelectorsMatchTheCapturedSection(t *testing.T) {
	team := fixture(t, "07_hiring_team.html")
	if count(t, team, HiringTeamSectionXPath) != 1 {
		t.Fatal("the 'People you can reach out to' section is not found")
	}
	section := one(t, team, HiringTeamSectionXPath)
	link := one(t, section, HirerProfileLinkXPath)
	if htmlquery.SelectAttr(link, "href") != "https://www.linkedin.com/in/ana-ribeiro-fictional/" {
		t.Errorf("hirer profile href %q", htmlquery.SelectAttr(link, "href"))
	}
	msg := one(t, section, HirerMessageXPath)
	if htmlquery.SelectAttr(msg, "aria-label") != "Message" || !strings.HasPrefix(htmlquery.SelectAttr(msg, "href"), "/messaging/compose/") {
		t.Error("the free Message control")
	}
	card := one(t, section, HirerCardXPath)
	if count(t, card, HirerProfileLinkXPath) != 1 || count(t, card, HirerMessageXPath) != 1 {
		t.Error("the card does not hold the hirer's profile and its Message control together")
	}
	if text(one(t, card, HirerNameXPath)) != "Ana Ribeiro" {
		t.Errorf("hirer name %q", text(one(t, card, HirerNameXPath)))
	}
}

// The search page's details pane draws the hiring team in its older layout: no
// slot id, a Message button with no href, and two profile links per hirer (the
// photo and the name).
func TestHiringTeamSelectorsMatchTheSearchPane(t *testing.T) {
	pane := fixture(t, "11_search_pane_hiring_team.html")
	if count(t, pane, HiringTeamSectionXPath) != 1 {
		t.Fatal("the 'Meet the hiring team' section is not found")
	}
	section := one(t, pane, HiringTeamSectionXPath)
	if count(t, section, HirerMessageXPath) != 1 {
		t.Fatal("the Message button is not found")
	}
	card := one(t, section, HirerCardXPath)
	if count(t, card, HirerMessageXPath) != 1 {
		t.Error("the card does not hold the Message button")
	}
	for _, link := range htmlquery.Find(card, HirerProfileLinkXPath) { // the photo and the name
		if href := htmlquery.SelectAttr(link, "href"); href != "https://www.linkedin.com/in/bruno-tavares-fictional" {
			t.Errorf("hirer profile href %q", href)
		}
	}
	if text(one(t, card, HirerNameXPath)) != "Bruno Tavares" {
		t.Errorf("hirer name %q", text(one(t, card, HirerNameXPath)))
	}
}

// The search pane's Message control since 2026-10-21 is a button with the text
// "Message" and no send-privately icon (fixture 14), and the pane draws a
// "People you can reach out to" section before the hiring team.
func TestHiringTeamSelectorsMatchThePaneWithATextMessageButton(t *testing.T) {
	pane := fixture(t, "14_search_pane_text_message_button.html")
	if count(t, pane, HiringTeamSectionXPath) != 2 {
		t.Fatalf("hiring team sections = %d, want 2 (connections, then the hiring team)", count(t, pane, HiringTeamSectionXPath))
	}
	var hiring *html.Node
	for _, section := range htmlquery.Find(pane, HiringTeamSectionXPath) {
		if count(t, section, HirerInSectionXPath) > 0 || count(t, section, HirerProfileLinkXPath) > 0 {
			hiring = section
		}
	}
	if hiring == nil {
		t.Fatal("no section holds a hirer's profile link")
	}
	if count(t, hiring, HirerMessageXPath) != 1 {
		t.Fatal("the text Message button is not found")
	}
	card := one(t, hiring, HirerCardXPath)
	if count(t, card, HirerMessageXPath) != 1 || count(t, card, HirerProfileLinkXPath) != 2 {
		t.Error("the card does not hold the hirer's profile links and its Message button together")
	}
	if text(one(t, card, HirerNameXPath)) != "Bruno Tavares" {
		t.Errorf("hirer name %q", text(one(t, card, HirerNameXPath)))
	}
}

func TestHiringTeamSelectorsOnAnEmptySlot(t *testing.T) {
	team := fixture(t, "10_no_hiring_team.html")
	if count(t, team, HiringTeamSectionXPath) != 1 {
		t.Fatal("the empty section is not found")
	}
	section := one(t, team, HiringTeamSectionXPath)
	for _, xp := range []string{HirerProfileLinkXPath, HirerMessageXPath, HirerCardXPath} {
		if count(t, section, xp) != 0 {
			t.Errorf("%s matches an empty hiring team", xp)
		}
	}
}

// HirerInSectionXPath is what the job page waits for: the slot is drawn empty
// first (10) and filled later (07, and the search pane's 11).
func TestHirerInSectionMatchesOnlyAFilledHiringTeam(t *testing.T) {
	for name, filled := range map[string]bool{"07_hiring_team.html": true, "11_search_pane_hiring_team.html": true, "10_no_hiring_team.html": false} {
		if n := count(t, fixture(t, name), HirerInSectionXPath); (n > 0) != filled {
			t.Errorf("%s: %d hirer links in the hiring team, want them: %v", name, n, filled)
		}
	}
}

// A hirer card never climbs out of the hiring team section. Here the only hirer
// has no Message control in its section, but a send-privately button sits
// elsewhere in the pane: that button must not be paired with the hirer.
func TestAHirerCardStaysInsideTheHiringTeamSection(t *testing.T) {
	doc, err := htmlquery.Parse(strings.NewReader(`<body><div class="pane">
		<div id="JobDetailsPeopleWhoCanHelpSlot_1"><div class="hirer"><a href="https://www.linkedin.com/in/ana-ribeiro-fictional/"><p>Ana Ribeiro</p></a></div></div>
		<div class="actions"><button><span data-test-icon="send-privately-small"></span></button></div>
		</div></body>`))
	if err != nil {
		t.Fatal(err)
	}
	section := one(t, doc, HiringTeamSectionXPath)
	if n := count(t, section, HirerCardXPath); n != 0 {
		t.Errorf("%d hirer cards inside a section with no Message control; want none", n)
	}
}

// ------------------------------------------------------------ message overlay (08, 09)
// In the live page the overlay sits in the open shadow root of #interop-outlet;
// the fixtures hold its markup alone, so the selectors inside it are checked
// here directly and the host on a page like LinkedIn's.
func TestMessageOutletSelectorFindsTheShadowHost(t *testing.T) {
	doc, err := htmlquery.Parse(strings.NewReader(`<body><div id="interop-outlet" data-testid="interop-shadowdom"></div><div id="other"></div></body>`))
	if err != nil {
		t.Fatal(err)
	}
	if count(t, doc, MessageOutletXPath) != 1 {
		t.Error("the messaging overlay's shadow host")
	}
}

func TestComposeSelectorsMatchTheCapturedFreeInMail(t *testing.T) {
	overlay := fixture(t, "08_message_overlay.html")
	bubble := one(t, overlay, ComposeBubbleXPath)
	if htmlquery.SelectAttr(one(t, bubble, ComposeRecipientXPath), "href") != "/in/ana-ribeiro-fictional/" {
		t.Error("the compose bubble's recipient link")
	}
	box := one(t, bubble, MessageBoxXPath)
	if htmlquery.SelectAttr(box, "aria-label") != "Write a message…" {
		t.Errorf("message box %q", htmlquery.SelectAttr(box, "aria-label"))
	}
	send := one(t, bubble, MessageSendButtonXPath)
	if _, disabled := attr(send, "disabled"); !disabled {
		t.Error("the send button is disabled before anything is typed")
	}
	if !strings.Contains(text(one(t, bubble, InMailCreditsXPath)), FreeMessageText) {
		t.Errorf("credits display %q does not say %q", text(one(t, bubble, InMailCreditsXPath)), FreeMessageText)
	}
	if htmlquery.SelectAttr(one(t, bubble, MessageSubjectXPath), "placeholder") != "Subject (optional)" {
		t.Error("the optional subject")
	}
	if text(one(t, bubble, BubbleCloseButtonXPath)) != "Close your draft conversation" {
		t.Error("the draft's close button")
	}
	if count(t, overlay, UpsellModalXPath) != 0 {
		t.Error("the upsell selector matches a compose")
	}
}

func TestUpsellSelectorsMatchTheCapturedPremiumModal(t *testing.T) {
	upsell := fixture(t, "09_inmail_compose.html")
	modal := one(t, upsell, UpsellModalXPath)
	one(t, modal, UpsellDismissXPath)
	for _, xp := range []string{ComposeBubbleXPath, MessageBoxXPath, MessageSendButtonXPath} {
		if count(t, upsell, FromDocument(xp)) != 0 {
			t.Errorf("%s matches the upsell", xp)
		}
	}
}

func TestUpsellSelectorsMatchTheSearchPageUpsell(t *testing.T) {
	doc := fixture(t, "13_search_upsell.html")
	modal := one(t, doc, UpsellModalXPath)
	one(t, modal, UpsellDismissXPath)
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

// attr is an attribute and whether it is there.
func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

// ------------------------------------------------------------ connect (15 to 20)
func TestConnectSelectorsOnTheMoreMenuOfAThirdDegreeProfile(t *testing.T) {
	doc := fixture(t, "15_profile_more_connect.html")
	one(t, doc, ProfileMoreXPath)
	if count(t, doc, TopCardDegreeXPath) != 0 {
		t.Error("a 3rd-degree profile matched the 1st-degree text")
	}
	if count(t, doc, TopCardConnectXPath) != 0 {
		t.Error("Connect is in the top card of this profile; it is only in More")
	}
	one(t, doc, MenuXPath)
	item := one(t, doc, MenuConnectXPath)
	if got := text(item); got != "Connect" {
		t.Errorf("the Connect item reads %q", got)
	}
	if count(t, doc, MenuPendingXPath) != 0 {
		t.Error("a fresh Connect item matched Pending")
	}
}

func TestConnectSelectorsOnAFirstDegreeProfile(t *testing.T) {
	doc := fixture(t, "16_profile_connected.html")
	one(t, doc, TopCardDegreeXPath)
	one(t, doc, ProfileMoreXPath)
	if count(t, doc, TopCardConnectXPath) != 0 || count(t, doc, TopCardPendingXPath) != 0 {
		t.Error("a 1st-degree profile offers no invite")
	}
}

func TestConnectSelectorsOnATopCardConnectButton(t *testing.T) {
	doc := fixture(t, "20_profile_top_connect.html")
	one(t, doc, TopCardConnectXPath)
	one(t, doc, ProfileMoreXPath)
}

func TestInviteDialogSelectors(t *testing.T) {
	note := fixture(t, "17_invite_note_dialog.html")
	one(t, note, InviteSendWithoutNoteXPath)
	one(t, note, InviteDialogXPath)
	one(t, note, FromDocument(InviteDismissXPath))
	email := fixture(t, "18_invite_email_dialog.html")
	one(t, email, InviteEmailDialogXPath)
	one(t, email, FromDocument(InviteDismissXPath))
	limit := fixture(t, "19_invite_limit_notice.html")
	one(t, limit, InviteLimitDialogXPath)
	one(t, limit, FromDocument(InviteDismissXPath))
	if count(t, note, InviteEmailDialogXPath) != 0 || count(t, email, InviteLimitDialogXPath) != 0 {
		t.Error("an invite dialog matched another dialog's text")
	}
}

// TestConnectPendingSelectorsMatchAnInviteSent: once an invite is out, the top
// card's control and the More menu's item read Pending (synthetic relabelling
// of fixtures 20 and 15; LinkedIn's Pending label is not captured yet).
func TestConnectPendingSelectorsMatchAnInviteSent(t *testing.T) {
	const pending = `aria-label="Pending, click to withdraw invitation sent to Ana Ribeiro"`
	top, err := htmlquery.Parse(strings.NewReader(strings.ReplaceAll(readFixtureText(t, "20_profile_top_connect.html"),
		`aria-label="Invite Ana Ribeiro to connect"`, pending)))
	if err != nil {
		t.Fatal(err)
	}
	one(t, top, TopCardPendingXPath)
	if count(t, top, TopCardConnectXPath) != 0 {
		t.Error("a pending invite still offers Connect in the top card")
	}
	menu, err := htmlquery.Parse(strings.NewReader(strings.ReplaceAll(readFixtureText(t, "15_profile_more_connect.html"),
		`aria-label="Invite Ana Ribeiro to connect"`, pending)))
	if err != nil {
		t.Fatal(err)
	}
	one(t, menu, MenuPendingXPath)
	if count(t, menu, MenuConnectXPath) != 0 {
		t.Error("a pending invite still offers Connect on the menu")
	}
}

// readFixtureText is a fixture's text, for a test that edits it first.
func readFixtureText(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
