package cli

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/luizfreitas27/ajai/internal/config"
)

// initAnswers is what the wizard or the flags collect, as text; the config
// package turns it into typed essentials.
type initAnswers struct {
	firstName, lastName, phone, city, country string
	terms, location, market, resume, years    string
	salary                                    string
	keepPacing                                bool
	maxPerHour, maxPerRun                     string
}

func (a *App) initCmd() *cobra.Command {
	defaults := config.Defaults()
	ans := initAnswers{keepPacing: true}
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the config file with a short wizard (or from flags)",
		Long: `Writes a commented config.yaml with the essential settings. On a terminal with no
flags it asks interactively; with flags it writes without asking, which suits scripts
and tests. Every other setting starts at its documented default; edit the file or use
"ajai config set". Secrets are not asked for: put them in the .env file the command
points you to.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(a.paths.ConfigFile); err == nil && !force {
				return errors.New(a.paths.ConfigFile + " already exists; use --force to overwrite it")
			}
			if !cmd.Flags().Changed("first-name") && !cmd.Flags().Changed("terms") {
				if !a.stdinIsTerminal() {
					return errors.New("no terminal to ask questions on; pass --first-name, --last-name and --terms (see --help)")
				}
				if err := runWizard(&ans); err != nil {
					return err
				}
			}
			essentials, err := ans.essentials()
			if err != nil {
				return err
			}
			cfg := defaults
			if err := cfg.ApplyEssentials(essentials); err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			if err := config.Write(a.paths.ConfigFile, cfg); err != nil {
				return err
			}
			if err := a.paths.EnsureDataDirs(); err != nil {
				return err
			}
			a.openLog(cfg.Settings.LogLevel)
			a.logger.Info("config created", "path", a.paths.ConfigFile, "market", cfg.Markets.Active)
			a.ui.OK("config", a.paths.ConfigFile)
			a.ui.Println()
			a.ui.Title("Next steps")
			a.ui.Hint("1. Put your resume at " + a.paths.ResumeDir + "/" + cfg.Questions.Resume + " (or set questions.resume to an absolute path).")
			a.ui.Hint("2. Secrets go in " + a.paths.EnvFile + " as " + strings.Join(config.SecretEnvNames, ", ") + " (only the ones you use).")
			a.ui.Hint("3. Run `ajai doctor`, then `ajai run --dry-run`.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ans.firstName, "first-name", "", "your first name")
	f.StringVar(&ans.lastName, "last-name", "", "your last name")
	f.StringVar(&ans.phone, "phone", "", "phone number")
	f.StringVar(&ans.city, "city", "", "current city")
	f.StringVar(&ans.country, "country", "", "country")
	f.StringVar(&ans.terms, "terms", "", "search terms, comma-separated")
	f.StringVar(&ans.location, "location", "", "search location")
	f.StringVar(&ans.market, "market", defaults.Markets.Active, "active market: brasil or internacional")
	f.StringVar(&ans.resume, "resume", defaults.Questions.Resume, "resume file (relative to the resumes dir, or absolute)")
	f.StringVar(&ans.years, "years", defaults.Questions.YearsOfExperience, "total years of experience")
	f.StringVar(&ans.salary, "salary", "0", "desired salary (annual; monthly when --market brasil)")
	f.StringVar(&ans.maxPerHour, "max-per-hour", strconv.Itoa(defaults.Pacing.MaxPerHour), "pacing: applications per hour")
	f.StringVar(&ans.maxPerRun, "max-per-run", strconv.Itoa(defaults.Pacing.MaxPerRun), "pacing: applications per run")
	f.BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}

func (a *App) stdinIsTerminal() bool {
	f, ok := a.Stdin.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// essentials parses the text answers. Flag defaults already hold the config
// defaults, so only what the user typed differs.
func (ans initAnswers) essentials() (config.Essentials, error) {
	e := config.Essentials{
		FirstName: strings.TrimSpace(ans.firstName), LastName: strings.TrimSpace(ans.lastName),
		Phone: strings.TrimSpace(ans.phone), City: strings.TrimSpace(ans.city), Country: strings.TrimSpace(ans.country),
		Terms: ans.terms, Location: strings.TrimSpace(ans.location), Market: strings.TrimSpace(ans.market),
		Resume: strings.TrimSpace(ans.resume), YearsOfExperience: strings.TrimSpace(ans.years),
	}
	var err error
	if e.DesiredSalary, err = number("--salary", ans.salary); err != nil {
		return e, err
	}
	if e.MaxPerHour, err = number("--max-per-hour", ans.maxPerHour); err != nil {
		return e, err
	}
	if e.MaxPerRun, err = number("--max-per-run", ans.maxPerRun); err != nil {
		return e, err
	}
	return e, nil
}

func number(flag, text string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, errors.New(flag + ": " + strconv.Quote(text) + " is not a whole number")
	}
	return n, nil
}

func runWizard(ans *initAnswers) error {
	marketOptions := make([]huh.Option[string], 0, len(config.MarketNames))
	for _, m := range config.MarketNames {
		marketOptions = append(marketOptions, huh.NewOption(string(m), string(m)))
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("First name").Value(&ans.firstName).Validate(required),
			huh.NewInput().Title("Last name").Value(&ans.lastName).Validate(required),
			huh.NewInput().Title("Phone").Description("Digits only is safest").Value(&ans.phone),
			huh.NewInput().Title("City").Value(&ans.city),
			huh.NewInput().Title("Country").Value(&ans.country),
		).Title("About you"),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Market").
				Description("brasil: BRL, monthly salary. internacional: foreign currency, annual salary.").
				Options(marketOptions...).Value(&ans.market),
			huh.NewInput().Title("Search terms").Description("Comma-separated, e.g. Software Engineer, Python Developer").
				Value(&ans.terms).Validate(required),
			huh.NewInput().Title("Search location").Description("Blank uses the market's default").Value(&ans.location),
		).Title("What to look for"),
		huh.NewGroup(
			huh.NewInput().Title("Resume file").Description("Relative to the resumes directory, or an absolute path").Value(&ans.resume).Validate(required),
			huh.NewInput().Title("Years of experience").Value(&ans.years),
			huh.NewInput().Title("Desired salary").Description("A plain number, in the market's period").Value(&ans.salary).Validate(isNumber),
			huh.NewConfirm().Title("Keep the default pacing?").
				Description("15 applications/hour, 50/run, 30-90 s between jobs. Conservative on purpose (ADR 0001).").
				Value(&ans.keepPacing),
		).Title("Applying"),
		huh.NewGroup(
			huh.NewInput().Title("Applications per hour").Value(&ans.maxPerHour).Validate(isNumber),
			huh.NewInput().Title("Applications per run").Value(&ans.maxPerRun).Validate(isNumber),
		).Title("Pacing").WithHideFunc(func() bool { return ans.keepPacing }),
	)
	return form.Run()
}

func required(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func isNumber(s string) error {
	if _, err := strconv.Atoi(strings.TrimSpace(s)); err != nil {
		return errors.New("a whole number, please")
	}
	return nil
}
