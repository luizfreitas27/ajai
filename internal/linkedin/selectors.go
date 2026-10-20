// Package linkedin holds everything that depends on LinkedIn's markup. The
// selectors are deliberately centralised here and copied verbatim from the
// Python bot; selectors_test.go checks each against captured fixtures, so a
// LinkedIn restyle shows up as a failing test that names the dead locator.
//
// Lessons the Python project paid for, kept as rules:
//   - class names on cards are randomised; only semantic tokens and data-*
//     attributes are stable;
//   - "Sign in" must match exactly: "Sign in with Apple" sits above the real button;
//   - the apply button's size token (artdeco-button--3) is never a selector;
//   - Next/Review/Submit are looked up inside the dialog, never document-wide.
package linkedin

import (
	"strconv"
	"strings"
)

// Login page.
const (
	LoginEmailCSS      = "input[type='email']"
	LoginPasswordCSS   = "input[type='password']"
	SignInButtonXPath  = `//button[normalize-space(.)="Sign in"]`
	ForgotPasswordText = "Forgot password?"
)

// Job search results and cards.
const (
	JobCardsXPath            = "//li[@data-occludable-job-id]"
	JobIDAttribute           = "data-occludable-job-id"
	JobCardTitleLinkXPath    = ".//div[contains(@class,'artdeco-entity-lockup__title')]//a"
	JobCardCompanyXPath      = ".//div[contains(@class,'artdeco-entity-lockup__subtitle')]"
	JobCardLocationXPath     = ".//ul[contains(@class,'job-card-container__metadata-wrapper')]//span[@dir='ltr']"
	JobCardFooterStateClass  = "job-card-container__footer-job-state"
	JobCardEasyApplyXPath    = ".//ul[contains(@class,'footer-wrapper')]//span[normalize-space(.)='Easy Apply']"
	TitleVerificationSuffix  = " with verification"
	JobDescriptionClass      = "jobs-box__html-content"
	AboutCompanyClass        = "jobs-company__box"
	JobTopCardClasses        = "job-details-jobs-unified-top-card__primary-description-container,job-details-jobs-unified-top-card__primary-description,jobs-unified-top-card__primary-description,jobs-details__main-content"
	ApplyButtonXPath         = ".//button[@id='jobs-apply-button-id' or contains(@class,'jobs-apply-button')]"
	ApplyButtonID            = "jobs-apply-button-id"
	EasyApplyAriaLabelPrefix = "Easy Apply"
)

// EasyApplyLocators is the ordered fallback list for finding the apply button;
// the first that matches is clicked and the outcome (modal or new tab) decides
// whether the job is Easy Apply or external.
var EasyApplyLocators = []struct{ How, XPath string }{
	{"apply button id", ".//button[@id='jobs-apply-button-id']"},
	{"in-app apply URL flag", ".//a[contains(@href, 'openSDUIApplyFlow=true')]"},
	{"aria-label", ".//button[contains(@class,'jobs-apply-button') and contains(@aria-label, 'Easy Apply')]"},
	{"button label", ".//button[contains(@class,'jobs-apply-button')][.//span[contains(normalize-space(.), 'Easy Apply')]]"},
	{"apply button", ApplyButtonXPath},
}

// Easy Apply modal and its form.
const (
	ModalClass             = "jobs-easy-apply-modal"
	ModalXPath             = "//div[@role='dialog' and contains(@class,'jobs-easy-apply-modal')]"
	ModalCloseXPath        = ".//button[@data-test-modal-close-btn]"
	FormQuestionXPath      = ".//div[@data-test-form-element]"
	SelectXPath            = ".//select"
	SelectTitleXPath       = ".//label//span"
	RadioFieldsetXPath     = `.//fieldset[@data-test-form-builder-radio-button-form-component="true"]`
	RadioTitleXPath        = `.//span[@data-test-form-builder-radio-button-form-component__title]`
	RadioInputsXPath       = ".//input"
	TextInputXPath         = ".//input[@type='text']"
	TextareaXPath          = ".//textarea"
	CheckboxXPath          = ".//input[@type='checkbox']"
	LabelForXPath          = ".//label[@for]"
	HiddenTitleXPath       = ".//span[@class='visually-hidden']"
	VisuallyHiddenClass    = "visually-hidden"
	FollowCompanyCheckbox  = "follow-company-checkbox"
	ResumeUploadInputXPath = ".//input[@name='file']"
	TodayButtonXPath       = ".//button[contains(@aria-label, 'This is today')]"
	NextButtonXPath        = `.//button[@aria-label="Continue to next step" or contains(normalize-space(.), "Next")]`
	ReviewButtonXPath      = `.//button[@aria-label="Review your application" or normalize-space(.)="Review"]`
	SubmitButtonXPath      = `.//button[@aria-label="Submit application" or normalize-space(.)="Submit application"]`
	DiscardButtonXPath     = ".//button[@data-control-name='discard_application_confirm_btn' or contains(normalize-space(.), 'Discard')]"
)

// Search page controls and job page notices.
const (
	SearchLocationInputXPath = `.//input[@aria-label='City, state, or zip code' and not(@disabled)]`
	AllFiltersButtonXPath    = `//button[normalize-space()="All filters"]`
	ShowResultsButtonXPath   = `//button[contains(translate(@aria-label, "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz"), "apply current filters to show")]`
	AddCompanyInputXPath     = `(.//input[@placeholder='Add a company'])[1]`
	FilterSwitchXPath        = `.//input[@role="switch"]`
	PostedAgoXPath           = `//*[contains(@class,'jobs-unified-top-card') or contains(@class,'job-details-jobs-unified-top-card')]//span[contains(normalize-space(), " ago")]`
	// InlineErrorXPath, within a form question, is the message LinkedIn shows when
	// it refuses the question's answer ("Please enter a valid answer"). Not in a
	// capture yet (see testdata/fixtures/README.md).
	InlineErrorXPath = `.//*[contains(@class,'artdeco-inline-feedback--error')]`
	// FormProgressXPath, within the modal, is the region whose aria-label says how
	// far the application is ("…progress is at 0 percent.").
	FormProgressXPath     = `.//*[@role='region' and contains(@aria-label, 'progress is at')]`
	AppliedOnPageXPath    = `//*[contains(@class,'jobs-s-apply__application-link')]`
	PaginationActiveXPath = `.//button[contains(@class, 'active')]`
	// DailyLimitXPath is any element whose own text holds LinkedIn's notice that the
	// daily Easy Apply limit is reached, in any case: the inline one the Python bot
	// knew ("…exceeded the daily application limit…") or the one under a disabled
	// Easy Apply button since 2026-10 ("We limit daily submissions … apply tomorrow.").
	DailyLimitXPath = `//*[text()[contains(` + lowered + `, "exceeded the daily application limit") or contains(` + lowered + `, "limit daily submissions")]]`
	lowered         = `translate(., "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz")`
)

// The hiring team ("Meet the hiring team") in its two layouts: the job page's
// slot (fixtures 07 and 10), with an aria-label="Message" link to
// /messaging/compose/, and the search page's details pane (fixture 11), an older
// layout with no slot id whose Message control is a button with the
// send-privately icon and no href. HirerCardXPath, within the section, is the
// nearest element holding a Message control whose profile links all go to one
// hirer (the pane links the photo and the name), so a profile is never paired
// with another hirer's button. The card is also kept inside the section, so
// the climb never reaches a Message button outside it. HirerNameXPath is
// applied inside the card.
const (
	HiringTeamSectionXPath = `//div[starts-with(@id,"JobDetailsPeopleWhoCanHelpSlot_")] | //div[contains(@class,"job-details-people-who-can-help__section")]`
	HirerProfileLinkXPath  = `.//a[starts-with(@href,"https://www.linkedin.com/in/")]`
	HirerMessageXPath      = `.//a[@aria-label="Message" and starts-with(@href,"/messaging/compose/")] | .//button[.//*[@data-test-icon="send-privately-small"]]`
	HirerCardXPath         = `.//a[starts-with(@href,"https://www.linkedin.com/in/")]/ancestor::*[.//a[@aria-label="Message" and starts-with(@href,"/messaging/compose/")] or .//button[.//*[@data-test-icon="send-privately-small"]]][not(.//a[starts-with(@href,"https://www.linkedin.com/in/")]/@href != .//a[starts-with(@href,"https://www.linkedin.com/in/")]/@href)][ancestor-or-self::*[starts-with(@id,"JobDetailsPeopleWhoCanHelpSlot_") or contains(@class,"job-details-people-who-can-help__section")]][1]`
	HirerNameXPath         = `(.//a[starts-with(@href,"https://www.linkedin.com/in/")]//p | .//a[starts-with(@href,"https://www.linkedin.com/in/")]//strong)[1]`
)

// HirerInSectionXPath is a hirer's profile link inside the hiring team, which
// is what to wait for: the job page draws the slot before LinkedIn fills it,
// and leaves it empty on a job without a hiring team (fixture 10).
const HirerInSectionXPath = "(" + HiringTeamSectionXPath + `)//a[starts-with(@href,"https://www.linkedin.com/in/")]`

// The messaging overlay the hirer's Message link opens in place (fixtures 08 and
// 09). It lives in the open shadow root of MessageOutletXPath, out of reach of a
// document-wide XPath: the absolute selectors below are evaluated from that
// shadow root (browser.Element.ShadowElements), the relative ones inside the
// compose bubble or the upsell.
const (
	MessageOutletXPath = `//div[@id="interop-outlet"]`
	ComposeBubbleXPath = `//div[@role="dialog" and contains(@class,"msg-overlay-conversation-bubble--is-compose")]`
	// ComposeRecipientXPath is the profile link of the member the compose is to.
	ComposeRecipientXPath  = `.//a[contains(@class,"msg-compose__profile-link")]`
	MessageBoxXPath        = `.//div[contains(@class,"msg-form__contenteditable") and @contenteditable="true" and @role="textbox"]`
	MessageSendButtonXPath = `.//button[contains(@class,"msg-form__send-btn") and @type="submit"]`
	// MessageSubjectXPath is the subject of an InMail; a connection's compose has none.
	MessageSubjectXPath = `.//input[@name="subject"]`
	// InMailCreditsXPath says what an InMail costs; FreeMessageText in it means it costs nothing.
	InMailCreditsXPath = `.//section[contains(@class,"msg-inmail-credits-display")]`
	FreeMessageText    = "Free message"
	// BubbleCloseButtonXPath closes the bubble by its icon: the draft's button says
	// "Close your draft conversation", a conversation's names the member.
	BubbleCloseButtonXPath = `.//button[.//*[@data-test-icon="close-small"]]`
	UpsellModalXPath       = `//div[@data-test-modal-id="modal-upsell"]`
	UpsellDismissXPath     = `.//button[@aria-label="Dismiss" and @data-test-modal-close-btn]`
	// SentMessageBodyXPath is a message in a conversation's list. Not in a capture
	// yet (see testdata/fixtures/README.md): the send is confirmed by the box
	// emptying as well.
	SentMessageBodyXPath = `//*[contains(@class,"msg-s-event-listitem__body")]`
)

// PaginationClasses are tried in order to find the results pager.
var PaginationClasses = []string{"jobs-search-pagination__pages", "artdeco-pagination__pages", "artdeco-pagination"}

// ClassXPath matches any element carrying this class token (as a substring, like the Python contains()).
func ClassXPath(class string) string { return "//*[contains(@class,'" + class + "')]" }

// PageButtonXPath is the pager button for page n.
func PageButtonXPath(n int) string { return ".//button[@aria-label='Page " + strconv.Itoa(n) + "']" }

// Form-internal selectors the apply loop uses.
const (
	SelectOptionsXPath      = ".//option"
	VisuallyHiddenAnyXPath  = ".//*[contains(@class,'visually-hidden')]"
	FollowCompanyInputXPath = ".//input[@id='follow-company-checkbox' and @type='checkbox']"
	FollowCompanyLabelXPath = ".//label[@for='follow-company-checkbox']"
	DoneButtonText          = "Done"
	ContinueButtonText      = "Continue"
)

// DialogButtonXPath is a button holding this text inside an open dialog,
// anywhere in the document. Kept to a dialog: a span anywhere holding the word
// (a company called "Software Delivery Done Right") must not stand in for it.
func DialogButtonXPath(text string) string {
	return "//*[@role='dialog']" + strings.TrimPrefix(TextXPath("button", text), ".")
}

// FromDocument turns a dialog-relative selector (".//x") into a document-wide one ("//x").
func FromDocument(xpath string) string { return strings.TrimPrefix(xpath, ".") }

// OptionLabelXPath is the label of a radio option with this input id.
func OptionLabelXPath(inputID string) string { return `.//label[@for="` + inputID + `"]` }

// OptionWrapLabelXPath is the label wrapping the input with this id, for an option
// whose label has no for attribute.
func OptionWrapLabelXPath(inputID string) string { return `.//label[.//input[@id="` + inputID + `"]]` }
