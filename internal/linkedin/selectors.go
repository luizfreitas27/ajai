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
	InlineFeedbackXPath      = `//*[contains(@class,'artdeco-inline-feedback__message')]`
	AppliedOnPageXPath       = `//*[contains(@class,'jobs-s-apply__application-link')]`
	PaginationActiveXPath    = `.//button[contains(@class, 'active')]`
	DailyLimitPhrase         = "exceeded the daily application limit"
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

// FromDocument turns a dialog-relative selector (".//x") into a document-wide one ("//x").
func FromDocument(xpath string) string { return strings.TrimPrefix(xpath, ".") }

// OptionLabelXPath is the label of a radio option with this input id.
func OptionLabelXPath(inputID string) string { return `.//label[@for="` + inputID + `"]` }
