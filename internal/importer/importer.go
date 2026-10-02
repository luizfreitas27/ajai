// Package importer brings a Python Auto_job_applier_linkedIn checkout into aja:
// user_config.json into the YAML config, answers_memory.json into the answers
// table, the two spreadsheets into the jobs table, and the resume file into the
// resumes directory. It never copies a secret; it reports which environment
// variables to set instead. Running it twice changes nothing.
package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/store"
	"github.com/luizfreitas27/aja/internal/textmatch"
)

// Report says what the import did and what it could not do.
type Report struct {
	Mapped         []string          // "python.key -> aja.key"
	Unmapped       map[string]string // python.key -> why
	SecretsSeen    []string          // aja environment variables the user must set by hand
	ResumeCopied   string            // destination path, "" when not copied
	ResumeMissing  string            // the Python path that could not be found
	ResumeError    string            // the copy failed: why
	Answers        int               // answers imported
	AnswersKept    int               // answers skipped because aja's copy is newer
	Applied        int
	External       int
	Failed         int
	JobsKept       int // job rows skipped because a newer record already exists
	ConfigWritten  bool
	ConfigProblems *config.ValidationError // the written config still needs these fixes
}

// Python file names inside the checkout.
const (
	userConfigFile = "user_config.json"
	answersFile    = "answers_memory.json"
	envFile        = ".env"
	excelsDir      = "all excels"
)

// pythonEnvNames maps the Python .env variables to aja's. LinkedIn credentials
// have no counterpart: aja never logs in with a password.
var pythonEnvNames = map[string]string{
	"LLM_API_KEY":        config.EnvAIAPIKey,
	"TELEGRAM_BOT_TOKEN": config.EnvTelegramBotToken,
	"TELEGRAM_CHAT_ID":   config.EnvTelegramChatID,
}

// mapping is one Python key's destination; convert may reshape the value.
type mapping struct {
	key     string
	convert func(any) any
}

// superseded explains Python keys that have no aja counterpart on purpose.
var superseded = map[string]string{
	"questions.pause_before_submit":      "replaced by `aja run --dry-run` and the help pause",
	"questions.pause_at_failed_question": "replaced by the help pause and `--unattended`",
	"settings.stop_before_submit":        "replaced by `aja run --dry-run`",
	"settings.run_in_background":         "aja never runs headless (ADR 0001)",
	"settings.safe_mode":                 "aja uses its own Chrome profile (sign in once with aja login); set settings.chrome_profile_dir to use another",
	"settings.keep_screen_awake":         "not needed: no desktop dialogs",
	"settings.auto_manage_driver":        "no WebDriver in aja",
	"settings.showAiErrorAlerts":         "no desktop dialogs; errors go to the log",
	"settings.generated_resume_path":     "resumes live in the aja resumes directory",
	"settings.file_name":                 "history lives in the SQLite database; `aja export --csv` writes the spreadsheet",
	"settings.failed_file_name":          "history lives in the SQLite database; `aja export --csv` writes the spreadsheet",
	"settings.logs_folder_path":          "logs live in the aja data directory",
	"settings.stealth_mode":              "no stealth library in aja (ADR 0001)",
	"settings.click_gap":                 config.RetiredKeys["settings.click_gap"],
	"settings.stop_date_cycle_at_24hr":   "a non-stop session always narrows the date filter to \"Past 24 hours\" and stays there, the Python default",
	"secrets.username":                   noPassword,
	"secrets.password":                   noPassword,
	"secrets.llm_temperature":            "not configurable in aja",
	"secrets.ai_provider":                "folded into ai.provider together with llm_api_url",
	"secrets.llm_api_url":                "folded into ai.provider / ai.base_url",
}

// noPassword is why the Python bot's LinkedIn credentials are not imported.
const noPassword = "aja never logs in with a password; sign in once with aja login"

// secrets are Python keys whose VALUE must never be copied; the user sets the variable.
var secrets = map[string]string{
	"secrets.llm_api_key":        config.EnvAIAPIKey,
	"secrets.telegram_bot_token": config.EnvTelegramBotToken,
	"secrets.telegram_chat_id":   config.EnvTelegramChatID,
}

var mappings = map[string]mapping{
	"personals.first_name":        {key: "personals.first_name"},
	"personals.middle_name":       {key: "personals.middle_name"},
	"personals.last_name":         {key: "personals.last_name"},
	"personals.phone_number":      {key: "personals.phone"},
	"personals.current_city":      {key: "personals.city"},
	"personals.street":            {key: "personals.street"},
	"personals.state":             {key: "personals.state"},
	"personals.zipcode":           {key: "personals.zip"},
	"personals.country":           {key: "personals.country"},
	"personals.ethnicity":         {key: "personals.ethnicity"},
	"personals.gender":            {key: "personals.gender"},
	"personals.disability_status": {key: "personals.disability"},
	"personals.veteran_status":    {key: "personals.veteran"},

	"questions.default_resume_path":        {key: "questions.resume", convert: func(v any) any { s, _ := v.(string); return filepath.Base(s) }},
	"questions.years_of_experience":        {key: "questions.years_of_experience"},
	"questions.require_visa":               {key: "questions.require_visa"},
	"questions.website":                    {key: "questions.website"},
	"questions.linkedIn":                   {key: "questions.linkedin"},
	"questions.legally_authorized":         {key: "questions.legally_authorized"},
	"questions.us_citizenship":             {key: "questions.citizenship"},
	"questions.desired_salary":             {key: "questions.desired_salary"},
	"questions.current_ctc":                {key: "questions.current_salary"},
	"questions.notice_period":              {key: "questions.notice_period"},
	"questions.linkedin_headline":          {key: "questions.headline"},
	"questions.linkedin_summary":           {key: "questions.summary"},
	"questions.cover_letter":               {key: "questions.cover_letter"},
	"questions.user_information_all":       {key: "questions.about"},
	"questions.recent_employer":            {key: "questions.recent_employer"},
	"questions.confidence_level":           {key: "questions.confidence_level"},
	"questions.overwrite_previous_answers": {key: "questions.overwrite_previous_answers"},

	"search.search_terms":                    {key: "search.terms"},
	"search.search_location":                 {key: "search.location"},
	"search.switch_number":                   {key: "search.switch_number"},
	"search.randomize_search_order":          {key: "search.randomize_order"},
	"search.sort_by":                         {key: "search.sort_by"},
	"search.date_posted":                     {key: "search.date_posted"},
	"search.salary":                          {key: "search.salary"},
	"search.easy_apply_only":                 {key: "search.easy_apply_only"},
	"search.experience_level":                {key: "search.experience_level"},
	"search.job_type":                        {key: "search.job_type"},
	"search.on_site":                         {key: "search.on_site"},
	"search.companies":                       {key: "search.companies"},
	"search.location":                        {key: "search.locations"},
	"search.industry":                        {key: "search.industry"},
	"search.job_function":                    {key: "search.job_function"},
	"search.job_titles":                      {key: "search.job_titles"},
	"search.benefits":                        {key: "search.benefits"},
	"search.commitments":                     {key: "search.commitments"},
	"search.under_10_applicants":             {key: "search.under_10_applicants"},
	"search.in_your_network":                 {key: "search.in_your_network"},
	"search.fair_chance_employer":            {key: "search.fair_chance_employer"},
	"search.pause_after_filters":             {key: "search.pause_after_filters"},
	"search.about_company_bad_words":         {key: "search.about_company_bad_words"},
	"search.about_company_good_words":        {key: "search.about_company_good_words"},
	"search.bad_words":                       {key: "search.bad_words"},
	"search.skip_non_sponsoring_jobs":        {key: "search.skip_non_sponsoring_jobs"},
	"search.sponsorship_offered_phrases":     {key: "search.sponsorship_offered_phrases"},
	"search.sponsorship_unavailable_phrases": {key: "search.sponsorship_unavailable_phrases"},
	"search.skip_jobs_without_sponsorship":   {key: "search.skip_jobs_without_sponsorship"},
	"search.security_clearance":              {key: "search.security_clearance"},
	"search.did_masters":                     {key: "search.did_masters"},
	"search.current_experience":              {key: "search.current_experience"},

	"settings.close_tabs":         {key: "settings.close_tabs"},
	"settings.follow_companies":   {key: "settings.follow_companies"},
	"settings.run_non_stop":       {key: "settings.run_non_stop"},
	"settings.alternate_sortby":   {key: "settings.alternate_sort_by"},
	"settings.cycle_date_posted":  {key: "settings.cycle_date_posted"},
	"settings.smooth_scroll":      {key: "settings.smooth_scroll"},
	"settings.disable_extensions": {key: "settings.disable_extensions"},
	"settings.log_level":          {key: "settings.log_level"},

	"secrets.use_AI":    {key: "ai.enabled"},
	"secrets.llm_model": {key: "ai.model"},

	"markets.market":                     {key: "markets.active"},
	"markets.br_currency":                {key: "markets.brasil.currency"},
	"markets.br_desired_salary_monthly":  {key: "markets.brasil.desired_salary"},
	"markets.br_current_salary_monthly":  {key: "markets.brasil.current_salary"},
	"markets.br_search_location":         {key: "markets.brasil.location"},
	"markets.br_search_terms":            {key: "markets.brasil.terms"},
	"markets.intl_currency":              {key: "markets.internacional.currency"},
	"markets.intl_desired_salary_annual": {key: "markets.internacional.desired_salary"},
	"markets.intl_current_salary_annual": {key: "markets.internacional.current_salary"},
	"markets.intl_search_location":       {key: "markets.internacional.location"},
	"markets.intl_search_terms":          {key: "markets.internacional.terms"},
}

// providerFromPython maps the Python ai_provider / llm_api_url pair to aja's
// provider and (when non-default) base_url.
func providerFromPython(provider, url string) (string, string) {
	p := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(provider), " ", ""))
	switch p {
	case "openai", "deepseek", "gemini", "ollama", "lmstudio", "vllm":
	case "google", "googlegenai":
		p = "gemini"
	default:
		p = "custom"
	}
	url = strings.TrimSpace(url)
	if p != "custom" && (url == "" || url == config.ProviderBaseURLs[p]) {
		return p, ""
	}
	if p == "custom" && url == "" {
		return "openai", ""
	}
	return p, url
}

// Import brings the checkout at src into aja: the config file (existing one or
// defaults, then written back when user_config.json was found), the store and
// the resumes directory. The config is written even when it still fails
// validation; Report.ConfigProblems says what to fix.
func Import(src string, p paths.Paths, st *store.Store) (*Report, error) {
	base, lerr := config.LoadFile(p) // the file alone: an override never ends up in it
	if base == nil {
		return nil, lerr
	}
	cfg := base.Config
	rep, err := Run(src, &cfg, st, p)
	if err != nil {
		return rep, err
	}
	if rep.ConfigWritten {
		if verr := cfg.Validate(); verr != nil {
			rep.ConfigProblems, _ = config.AsValidation(verr)
		}
		if err := config.Write(p.ConfigFile, cfg); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// Run performs the import from src (a Python checkout) into cfg (modified in
// place), the store and the resumes directory. cfg is written by the caller.
func Run(src string, cfg *config.Config, st *store.Store, p paths.Paths) (*Report, error) {
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", src)
	}
	rep := &Report{Unmapped: map[string]string{}}
	if err := importConfig(filepath.Join(src, userConfigFile), cfg, rep); err != nil {
		return rep, err
	}
	if err := noteDotenv(filepath.Join(src, envFile), rep); err != nil {
		return rep, err
	}
	copyResume(src, cfg, p, rep)
	if err := importAnswers(filepath.Join(src, answersFile), st, rep); err != nil {
		return rep, err
	}
	if err := importJobs(filepath.Join(src, excelsDir), st, rep); err != nil {
		return rep, err
	}
	sort.Strings(rep.Mapped)
	rep.SecretsSeen = uniqueSorted(rep.SecretsSeen)
	return rep, nil
}

// noteDotenv reads only the KEYS of the Python .env (never the values) and
// records which aja variables the user has to set.
func noteDotenv(path string, rep *Report) error {
	values, err := godotenv.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for pyName, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if ajaName, ok := pythonEnvNames[pyName]; ok {
			rep.SecretsSeen = append(rep.SecretsSeen, ajaName)
		} else if strings.HasPrefix(pyName, "LINKEDIN_") {
			rep.Unmapped[".env "+pyName] = noPassword
		}
	}
	return nil
}

func uniqueSorted(items []string) []string {
	seen := map[string]bool{}
	out := items[:0]
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	sort.Strings(out)
	return out
}

func importConfig(path string, cfg *config.Config, rep *Report) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		rep.Unmapped[userConfigFile] = "not found; the Python defaults were in use, so aja's defaults apply"
		return nil
	}
	if err != nil {
		return err
	}
	var sections map[string]map[string]any
	if err := json.Unmarshal(data, &sections); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var pyProvider, pyURL string
	for section, values := range sections {
		for key, value := range values {
			full := section + "." + key
			if env, ok := secrets[full]; ok {
				if s, _ := value.(string); strings.TrimSpace(s) != "" {
					rep.SecretsSeen = append(rep.SecretsSeen, env)
				}
				continue
			}
			switch full {
			case "secrets.ai_provider":
				pyProvider, _ = value.(string)
				continue
			case "secrets.llm_api_url":
				pyURL, _ = value.(string)
				continue
			}
			if why, ok := superseded[full]; ok {
				rep.Unmapped[full] = why
				continue
			}
			m, ok := mappings[full]
			if !ok {
				rep.Unmapped[full] = "no aja counterpart"
				continue
			}
			if m.convert != nil {
				value = m.convert(value)
			}
			if err := cfg.SetValue(m.key, value); err != nil {
				rep.Unmapped[full] = err.Error()
				continue
			}
			rep.Mapped = append(rep.Mapped, full+" -> "+m.key)
		}
	}
	if pyProvider != "" || pyURL != "" {
		provider, url := providerFromPython(pyProvider, pyURL)
		cfg.AI.Provider, cfg.AI.BaseURL = provider, url
		rep.Mapped = append(rep.Mapped, "secrets.ai_provider+llm_api_url -> ai.provider/ai.base_url")
	}
	rep.ConfigWritten = true
	return nil
}

// copyResume copies the Python resume into the resumes directory when the file
// can be found (as given, or relative to the checkout).
func copyResume(src string, cfg *config.Config, p paths.Paths, rep *Report) {
	// The Python path was reduced to its base name by the mapping; look for the
	// original under the usual Python locations.
	candidates := []string{
		filepath.Join(src, "all resumes", "default", cfg.Questions.Resume),
		filepath.Join(src, "all resumes", cfg.Questions.Resume),
		filepath.Join(src, cfg.Questions.Resume),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			dst := filepath.Join(p.ResumeDir, filepath.Base(c))
			if err := copyFile(c, dst); err != nil {
				rep.ResumeError = err.Error()
				return
			}
			rep.ResumeCopied = dst
			return
		}
	}
	rep.ResumeMissing = cfg.Questions.Resume
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

type pythonMemory struct {
	Version int               `json:"version"`
	Answers []json.RawMessage `json:"answers"`
}

type pythonAnswer struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Answer      string `json:"answer"`
	Source      string `json:"source"`
	State       string `json:"state"`
	Normalised  string `json:"normalised"`
	Uses        int    `json:"uses"`
	LastJobLink string `json:"last_job_link"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func importAnswers(path string, st *store.Store, rep *Report) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var mem pythonMemory
	if err := json.Unmarshal(data, &mem); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for i, raw := range mem.Answers {
		entry := fmt.Sprintf("%s entry %d", answersFile, i+1)
		var a pythonAnswer
		if err := json.Unmarshal(raw, &a); err != nil || a.Label == "" || a.Kind == "" {
			rep.Unmapped[entry] = "malformed entry skipped"
			continue
		}
		if a.Normalised == "" {
			a.Normalised = textmatch.Normalise(a.Label)
		}
		// aja's own copy of the same question wins when it is newer: a later
		// re-import must not undo approvals or use counts made here.
		existing, err := st.FindAnswer(a.Kind, a.Normalised)
		if err != nil {
			return err
		}
		if existing != nil && !newer(a.UpdatedAt, existing.UpdatedAt) {
			rep.AnswersKept++
			continue
		}
		if err := st.UpsertAnswer(store.Answer{ID: a.ID, Label: a.Label, Normalised: a.Normalised, Kind: a.Kind,
			Answer: a.Answer, Source: a.Source, State: a.State, Uses: a.Uses, LastJobLink: a.LastJobLink,
			CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}); err != nil {
			rep.Unmapped[entry] = err.Error()
			continue
		}
		rep.Answers++
	}
	return nil
}

// newer reports whether timestamp a is strictly after b. Unparseable or blank
// values count as oldest, so an existing dated record is never replaced by an
// undated one.
func newer(a, b string) bool {
	ta, okA := store.ParseTime(a)
	tb, okB := store.ParseTime(b)
	switch {
	case !okA:
		return false
	case !okB:
		return true
	default:
		return ta.After(tb)
	}
}

func importJobs(dir string, st *store.Store, rep *Report) error {
	rows, err := store.ReadCSV(filepath.Join(dir, store.AppliedCSVName))
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := importJob(st, store.JobFromAppliedRow(row), rep); err != nil {
			return err
		}
	}
	rows, err = store.ReadCSV(filepath.Join(dir, store.FailedCSVName))
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := importJob(st, store.JobFromFailedRow(row), rep); err != nil {
			return err
		}
	}
	return nil
}

// importJob stores a row unless the database already has a record of that job
// that is at least as recent: a job that failed and was applied to later stays
// applied whichever CSV is read first, and a re-import changes nothing.
func importJob(st *store.Store, j store.Job, rep *Report) error {
	if j.JobID == "" {
		return nil
	}
	existing, err := st.GetJob(j.JobID)
	if err != nil {
		return err
	}
	if existing != nil && !newer(j.RecordedAt, existing.RecordedAt) {
		rep.JobsKept++
		return nil
	}
	if err := st.UpsertJob(j); err != nil {
		return err
	}
	switch j.Outcome {
	case store.External:
		rep.External++
	case store.Failed:
		rep.Failed++
	default:
		rep.Applied++
	}
	return nil
}
