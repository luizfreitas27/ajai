// Package config defines ajai's configuration: the YAML file with one section per
// concern, the secrets that live only in the environment, the layering that
// builds one Config value per process, validation with user-facing messages, and
// the commented YAML writer.
//
// Struct tags drive everything: `yaml` names the key, `doc` is the comment the
// writer emits and the help text the validator quotes, `opts` lists the valid
// values for string fields with a closed set, `min` is the lower bound for ints.
package config

// Config is the whole YAML file. It never contains a secret.
type Config struct {
	Personals Personals `yaml:"personals" doc:"Who you are. Used to fill contact fields and demographic questions."`
	Questions Questions `yaml:"questions" doc:"Your standing answers to the questions LinkedIn forms ask."`
	Search    Search    `yaml:"search" doc:"What to search for and which jobs to skip."`
	Settings  Settings  `yaml:"settings" doc:"How the tool behaves: Chrome, tabs, pacing of the search itself."`
	Markets   Markets   `yaml:"markets" doc:"Values that differ between applying in Brazil and abroad. One market is active per run."`
	Pacing    Pacing    `yaml:"pacing" doc:"Limits that keep a run at a human rhythm. Reaching one ends the run cleanly."`
	Messages  Messages  `yaml:"messages" doc:"Recruiter messages sent after an application. Each one waits in a queue until you approve it with ajai messages approve."`
	AI        AI        `yaml:"ai" doc:"The language model that answers unrecognised questions. The API key is AJAI_AI_API_KEY in the environment or .env, never here."`
}

// Personals mirrors the Python config/personals.py.
type Personals struct {
	FirstName  string `yaml:"first_name" doc:"Your first name." example:"Ana"`
	MiddleName string `yaml:"middle_name" doc:"Middle name, or blank."`
	LastName   string `yaml:"last_name" doc:"Your last name." example:"Silva"`
	Phone      string `yaml:"phone" doc:"Phone number as you want it typed into forms, digits only is safest." example:"11987654321"`
	City       string `yaml:"city" doc:"Current city. Blank means the forms' own default." example:"São Paulo"`
	Street     string `yaml:"street" doc:"Street address, for the rare forms that ask."`
	State      string `yaml:"state" doc:"State or province." example:"SP"`
	Zip        string `yaml:"zip" doc:"Postal code." example:"01000-000"`
	Country    string `yaml:"country" doc:"Country." example:"Brazil"`
	Ethnicity  string `yaml:"ethnicity" doc:"Answer to ethnicity questions." opts:"Decline|Hispanic/Latino|American Indian or Alaska Native|Asian|Black or African American|Native Hawaiian or Other Pacific Islander|White|Other"`
	Gender     string `yaml:"gender" doc:"Answer to gender questions." opts:"Male|Female|Other|Decline|"`
	Disability string `yaml:"disability" doc:"Answer to disability questions." opts:"Yes|No|Decline"`
	Veteran    string `yaml:"veteran" doc:"Answer to veteran-status questions." opts:"Yes|No|Decline"`
}

// Questions mirrors config/questions.py. Salaries here are the general values; a
// market may override them.
type Questions struct {
	Resume            string   `yaml:"resume" doc:"Path to the resume to upload when the active market has none. Relative paths resolve against the resumes directory." example:"resume.pdf"`
	YearsOfExperience string   `yaml:"years_of_experience" doc:"Total years of experience, as text, for 'years of experience' questions." example:"5"`
	SkillYears        []string `yaml:"skill_years" doc:"Your years with each technology or role, as \"name: years\" (a whole number, 1 or more). They answer questions about one of them, in English or Portuguese: how many years (the smallest when a question names several), and Yes/No questions about experience or knowledge with it (yes, never no). A question naming none of them is left to the answer memory, the AI and the review queue, as before." example:"java: 3,.net: 5,computação em nuvem: 3"`
	RequireVisa       string   `yaml:"require_visa" doc:"Do you need visa sponsorship?" opts:"Yes|No"`
	Website           string   `yaml:"website" doc:"Portfolio or GitHub URL."`
	LinkedIn          string   `yaml:"linkedin" doc:"Your LinkedIn profile URL."`
	LegallyAuthorized string   `yaml:"legally_authorized" doc:"Are you legally authorised to work where you apply?" opts:"Yes|No"`
	Citizenship       string   `yaml:"citizenship" doc:"Answer to US work-authorisation status questions." opts:"U.S. Citizen/Permanent Resident|Non-citizen allowed to work for any employer|Non-citizen allowed to work for current employer|Non-citizen seeking work authorization|Canadian Citizen/Permanent Resident|Other"`
	DesiredSalary     int      `yaml:"desired_salary" doc:"Desired annual salary, a plain number. Overridden by the active market when it sets one." min:"0"`
	CurrentSalary     int      `yaml:"current_salary" doc:"Current annual salary, a plain number. Overridden by the active market when it sets one." min:"0"`
	NoticePeriod      int      `yaml:"notice_period" doc:"Notice period in days." min:"0"`
	Headline          string   `yaml:"headline" doc:"One-line headline for 'headline' or 'summary' fields."`
	Summary           string   `yaml:"summary" doc:"Longer summary for textarea questions about you."`
	CoverLetter       string   `yaml:"cover_letter" doc:"Cover letter text for forms that ask for one."`
	About             string   `yaml:"about" doc:"Free text about you that the AI reads to answer questions (skills, experience, preferences). Never include secrets."`
	RecentEmployer    string   `yaml:"recent_employer" doc:"Most recent employer, or 'Not Applicable'."`
	ConfidenceLevel   string   `yaml:"confidence_level" doc:"Answer for 'rate yourself 1-10' questions." example:"8"`
	OverwritePrevious bool     `yaml:"overwrite_previous_answers" doc:"Overwrite values LinkedIn prefilled from earlier applications."`
}

// Search mirrors config/search.py.
type Search struct {
	Terms                         []string `yaml:"terms" doc:"Search terms, one run visits each. Overridden by the active market when it sets some." example:"Software Engineer,Python Developer"`
	Location                      string   `yaml:"location" doc:"Location typed into the search. Overridden by the active market when it sets one." example:"United States"`
	SwitchNumber                  int      `yaml:"switch_number" doc:"Move to the next search term after this many applications." min:"1"`
	RandomizeOrder                bool     `yaml:"randomize_order" doc:"Visit the search terms in random order."`
	SortBy                        string   `yaml:"sort_by" doc:"LinkedIn sort filter." opts:"|Most recent|Most relevant"`
	DatePosted                    string   `yaml:"date_posted" doc:"LinkedIn date filter." opts:"|Any time|Past month|Past week|Past 24 hours"`
	Salary                        string   `yaml:"salary" doc:"LinkedIn salary filter text, blank for none."`
	EasyApplyOnly                 bool     `yaml:"easy_apply_only" doc:"Only show Easy Apply jobs. Keep on; the tool cannot apply elsewhere."`
	ExperienceLevel               []string `yaml:"experience_level" doc:"LinkedIn experience filter values." opts:"Internship|Entry level|Associate|Mid-Senior level|Director|Executive"`
	JobType                       []string `yaml:"job_type" doc:"LinkedIn job type filter values." opts:"Full-time|Part-time|Contract|Temporary|Volunteer|Internship|Other"`
	OnSite                        []string `yaml:"on_site" doc:"LinkedIn workplace filter values." opts:"On-site|Remote|Hybrid"`
	Companies                     []string `yaml:"companies" doc:"Company filter values, exact LinkedIn names."`
	Locations                     []string `yaml:"locations" doc:"Location filter values."`
	Industry                      []string `yaml:"industry" doc:"Industry filter values."`
	JobFunction                   []string `yaml:"job_function" doc:"Job function filter values."`
	JobTitles                     []string `yaml:"job_titles" doc:"Title filter values."`
	Benefits                      []string `yaml:"benefits" doc:"Benefits filter values."`
	Commitments                   []string `yaml:"commitments" doc:"Commitments filter values."`
	Under10Applicants             bool     `yaml:"under_10_applicants" doc:"Only jobs with fewer than 10 applicants."`
	InYourNetwork                 bool     `yaml:"in_your_network" doc:"Only jobs in your network."`
	FairChanceEmployer            bool     `yaml:"fair_chance_employer" doc:"Only fair-chance employers."`
	PauseAfterFilters             bool     `yaml:"pause_after_filters" doc:"In an attended run, pause after applying filters so you can adjust them by hand."`
	AboutCompanyBadWords          []string `yaml:"about_company_bad_words" doc:"Skip the job when its 'About the company' text contains any of these."`
	AboutCompanyGoodWords         []string `yaml:"about_company_good_words" doc:"Do not skip for a bad word when one of these is also present."`
	BadWords                      []string `yaml:"bad_words" doc:"Skip the job when its title or description contains any of these."`
	SkipNonSponsoringJobs         bool     `yaml:"skip_non_sponsoring_jobs" doc:"Skip jobs whose description says they will not sponsor."`
	SponsorshipOfferedPhrases     []string `yaml:"sponsorship_offered_phrases" doc:"Phrases meaning sponsorship is available."`
	SponsorshipUnavailablePhrases []string `yaml:"sponsorship_unavailable_phrases" doc:"Phrases meaning sponsorship is not available."`
	SkipJobsWithoutSponsorship    bool     `yaml:"skip_jobs_without_sponsorship" doc:"Skip jobs that do not state sponsorship is available."`
	SecurityClearance             bool     `yaml:"security_clearance" doc:"Do you hold a security clearance? False skips jobs that require one."`
	DidMasters                    bool     `yaml:"did_masters" doc:"Do you hold a master's degree? Used by the experience heuristics."`
	CurrentExperience             int      `yaml:"current_experience" doc:"Years of experience used to skip jobs that ask for more. -1 disables the check." min:"-1"`
}

// Settings mirrors the surviving parts of config/settings.py.
type Settings struct {
	ChromeBinary      string `yaml:"chrome_binary" doc:"Path to the Chrome or Chromium executable. Blank finds it on this machine."`
	ChromeProfileDir  string `yaml:"chrome_profile_dir" doc:"Chrome user-data directory to use. Blank uses the ajai profile, in the data directory (sign in once with ajai login), so your everyday Chrome stays free; set your everyday profile here to use that instead."`
	CloseTabs         bool   `yaml:"close_tabs" doc:"Close the tabs the run opened when it ends."`
	FollowCompanies   bool   `yaml:"follow_companies" doc:"Leave the 'follow company' checkbox ticked when applying."`
	RunNonStop        bool   `yaml:"run_non_stop" doc:"Start another run, ten minutes later, when one visits every search term or reaches the per-run cap; stop at LinkedIn's daily limit, the hourly cap, --max or an error."`
	AlternateSortBy   bool   `yaml:"alternate_sort_by" doc:"Non-stop: flip the sort order before each later run (Most relevant <-> Most recent)."`
	CycleDatePosted   bool   `yaml:"cycle_date_posted" doc:"Non-stop: use the date-posted filter \"Past 24 hours\" for every later run."`
	SmoothScroll      bool   `yaml:"smooth_scroll" doc:"Scroll elements into view smoothly instead of jumping, before reading or clicking them."`
	DisableExtensions bool   `yaml:"disable_extensions" doc:"Start Chrome with extensions disabled."`
	LogLevel          string `yaml:"log_level" doc:"Level for the log file." opts:"DEBUG|INFO|WARN|ERROR"`
}

// Markets mirrors config/markets.py: the active market and the two sets of values.
type Markets struct {
	Active        string `yaml:"active" doc:"Which market this run applies in." opts:"brasil|internacional"`
	Brasil        Market `yaml:"brasil" doc:"Brazil: salaries are MONTHLY. Blank values fall back to the general questions/search settings (desired salary becomes the annual value divided by 12)."`
	Internacional Market `yaml:"internacional" doc:"Abroad: salaries are ANNUAL. Blank values fall back to the general questions/search settings."`
}

// Market is one market's values. Zero or blank means "fall back".
type Market struct {
	Currency        string   `yaml:"currency" doc:"ISO 4217 code typed when a form asks for the currency." example:"BRL"`
	DesiredSalary   int      `yaml:"desired_salary" doc:"Desired salary in this market's period, 0 to fall back." min:"0"`
	CurrentSalary   int      `yaml:"current_salary" doc:"Current salary in this market's period, 0 to fall back." min:"0"`
	Location        string   `yaml:"location" doc:"Search location for this market, blank to fall back."`
	Terms           []string `yaml:"terms" doc:"Search terms for this market, empty to fall back."`
	Resume          string   `yaml:"resume" doc:"Resume to upload in this market, blank to use questions.resume."`
	MessageTemplate string   `yaml:"message_template" doc:"Recruiter message for this market. Placeholders: {first_name} (blank name drops the name from the greeting), {title}, {company}. Blank sends nothing."`
}

// Pacing is new in ajai; see ADR 0001.
type Pacing struct {
	MaxPerHour        int `yaml:"max_applications_per_hour" doc:"Applications per rolling hour before the run ends." min:"1"`
	MaxPerRun         int `yaml:"max_per_run" doc:"Applications per run before it ends." min:"1"`
	BetweenJobsMin    int `yaml:"between_jobs_min_seconds" doc:"Shortest pause between two jobs, seconds." min:"0"`
	BetweenJobsMax    int `yaml:"between_jobs_max_seconds" doc:"Longest pause between two jobs, seconds." min:"0"`
	BetweenActionsMin int `yaml:"between_actions_min_seconds" doc:"Shortest pause between two form actions, seconds." min:"0"`
	BetweenActionsMax int `yaml:"between_actions_max_seconds" doc:"Longest pause between two form actions, seconds." min:"0"`
	TypingDelayMinMs  int `yaml:"typing_delay_min_ms" doc:"Shortest delay between typed characters, milliseconds." min:"0"`
	TypingDelayMaxMs  int `yaml:"typing_delay_max_ms" doc:"Longest delay between typed characters, milliseconds." min:"0"`
}

// Messages mirrors the recruiter message settings; see ADR 0002.
type Messages struct {
	Enabled           bool             `yaml:"enabled" doc:"After each submitted application, queue a message to the hirer shown on the job page, when a free Message button is there. Nothing is sent until you approve it (ajai messages list, ajai messages approve). Off by default."`
	MaxPerDay         int              `yaml:"max_per_day" doc:"Most messages sent per local day." min:"1"`
	BetweenMinSeconds int              `yaml:"between_min_seconds" doc:"Shortest pause before a message is sent, seconds." min:"0"`
	BetweenMaxSeconds int              `yaml:"between_max_seconds" doc:"Longest pause before a message is sent, seconds." min:"0"`
	Templates         MessageTemplates `yaml:"templates" doc:"Recruiter message in the job posting's language, detected from its title and description. A blank one falls back to the active market's message_template."`
}

// MessageTemplates holds one recruiter message template per language. The same
// placeholders as a market's message_template apply.
type MessageTemplates struct {
	En string `yaml:"en" doc:"Template for postings in English. Placeholders: {first_name}, {title}, {company}."`
	Pt string `yaml:"pt" doc:"Template for postings in Portuguese. Placeholders: {first_name}, {title}, {company}."`
	Es string `yaml:"es" doc:"Template for postings in Spanish. Placeholders: {first_name}, {title}, {company}."`
}

// AI selects the model. Every provider speaks the OpenAI-compatible chat API.
type AI struct {
	Enabled  bool   `yaml:"enabled" doc:"Ask a language model when no configured or remembered answer fits. Sensitive questions are never asked."`
	Provider string `yaml:"provider" doc:"Which OpenAI-compatible endpoint to use. 'custom' uses base_url as given." opts:"openai|deepseek|gemini|ollama|lmstudio|vllm|custom"`
	Model    string `yaml:"model" doc:"Model name as the provider knows it." example:"gpt-5.6-sol"`
	BaseURL  string `yaml:"base_url" doc:"Endpoint base URL. Blank uses the provider's default."`
}

// Defaults is the built-in configuration, the lowest layer.
func Defaults() Config {
	return Config{
		Personals: Personals{Ethnicity: "Decline", Gender: "Decline", Disability: "Decline", Veteran: "Decline"},
		Questions: Questions{
			Resume: "resume.pdf", YearsOfExperience: "5", RequireVisa: "No", LegallyAuthorized: "Yes",
			Citizenship: "Other", NoticePeriod: 30, RecentEmployer: "Not Applicable", ConfidenceLevel: "8",
			SkillYears: []string{},
		},
		Search: Search{
			Terms: []string{}, SwitchNumber: 30, DatePosted: "Past week", EasyApplyOnly: true,
			ExperienceLevel: []string{}, JobType: []string{}, OnSite: []string{}, Companies: []string{},
			Locations: []string{}, Industry: []string{}, JobFunction: []string{}, JobTitles: []string{},
			Benefits: []string{}, Commitments: []string{}, PauseAfterFilters: true,
			AboutCompanyBadWords: []string{}, AboutCompanyGoodWords: []string{}, BadWords: []string{},
			SponsorshipOfferedPhrases: []string{"visa sponsorship available", "sponsorship available",
				"sponsorship is available", "we sponsor", "will sponsor", "can sponsor", "offers sponsorship"},
			SponsorshipUnavailablePhrases: []string{"will not sponsor", "do not sponsor", "does not sponsor",
				"cannot sponsor", "unable to sponsor", "no sponsorship", "without sponsorship", "not able to sponsor"},
			DidMasters: false, CurrentExperience: -1,
		},
		Settings: Settings{AlternateSortBy: true, CycleDatePosted: true, LogLevel: "INFO"},
		Markets: Markets{
			Active: string(MarketInternacional),
			Brasil: Market{Currency: "BRL", Location: "Brasil", Terms: []string{},
				MessageTemplate: "Olá {first_name}, acabei de me candidatar à vaga de {title} na {company}. Fico à disposição para conversar e no aguardo de um contato. Obrigado!"},
			Internacional: Market{Currency: "USD", Terms: []string{},
				MessageTemplate: "Hi {first_name}, I just applied for the {title} role at {company}. I'd be glad to talk whenever it suits you, and I look forward to hearing from you. Thanks!"},
		},
		Pacing: Pacing{MaxPerHour: 15, MaxPerRun: 50, BetweenJobsMin: 30, BetweenJobsMax: 90,
			BetweenActionsMin: 1, BetweenActionsMax: 4, TypingDelayMinMs: 40, TypingDelayMaxMs: 160},
		Messages: Messages{MaxPerDay: 3, BetweenMinSeconds: 90, BetweenMaxSeconds: 300, Templates: MessageTemplates{
			En: "Hi {first_name}, I just applied for the {title} role at {company}. I'm available for any further information and would be glad to talk whenever it suits you. Looking forward to hearing from you!",
			Pt: "Olá {first_name}, acabei de me candidatar à vaga de {title} na {company}. Fico à disposição para mais informações e para conversarmos quando for melhor para você. No aguardo do seu contato!",
			Es: "Hola {first_name}, acabo de postularme a la vacante de {title} en {company}. Quedo a disposición para cualquier información adicional y para conversar cuando te venga bien. ¡Quedo atento a tu respuesta!",
		}},
		AI: AI{Provider: "openai", Model: "gpt-5.6-sol"},
	}
}

// ProviderBaseURLs are the defaults for ai.base_url per provider.
var ProviderBaseURLs = map[string]string{
	"openai":   "https://api.openai.com/v1/",
	"deepseek": "https://api.deepseek.com/v1/",
	"gemini":   "https://generativelanguage.googleapis.com/v1beta/openai/",
	"ollama":   "http://localhost:11434/v1/",
	"lmstudio": "http://localhost:1234/v1/",
	"vllm":     "http://localhost:8000/v1/",
}

// ProvidersWithoutKey run locally and need no API key.
var ProvidersWithoutKey = map[string]bool{"ollama": true, "lmstudio": true, "vllm": true}
