package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/config"
)

// initAnswers are the essentials the wizard (or the flags) collect.
type initAnswers struct {
	firstName, lastName, phone, city, country string
	terms, location, market, resume           string
	years                                     string
	salary                                    string
	acceptPacing                              bool
}

func (a *App) initCmd() *cobra.Command {
	var ans initAnswers
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the config file with a short wizard (or from flags)",
		Long: `Writes a commented config.yaml with the essential settings. On a terminal with no
flags it asks interactively; with flags it writes without asking, which suits scripts
and tests. Every other setting starts at its documented default; edit the file or use
"aja config set". Secrets are not asked for: put them in the .env file the command
points you to.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(a.paths.ConfigFile); err == nil && !force {
				return errors.New(a.paths.ConfigFile + " already exists; use --force to overwrite it")
			}
			interactive := !cmd.Flags().Changed("first-name") && !cmd.Flags().Changed("terms")
			if interactive {
				if !a.stdinIsTerminal() {
					return errors.New("no terminal to ask questions on; pass --first-name, --last-name and --terms (see --help)")
				}
				if err := a.wizard(&ans); err != nil {
					return err
				}
			}
			cfg, err := applyAnswers(config.Defaults(), ans)
			if err != nil {
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
			a.ui.OK("config", a.paths.ConfigFile)
			a.ui.Println()
			a.ui.Title("Next steps")
			a.ui.Hint("1. Put your resume at " + a.paths.ResumeDir + "/" + cfg.Questions.Resume + " (or set questions.resume to an absolute path).")
			a.ui.Hint("2. Secrets go in " + a.paths.EnvFile + " as " + strings.Join(config.SecretEnvNames, ", ") + " (only the ones you use).")
			a.ui.Hint("3. Run `aja doctor`, then `aja run --dry-run`.")
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
	f.StringVar(&ans.market, "market", "internacional", "active market: brasil or internacional")
	f.StringVar(&ans.resume, "resume", "resume.pdf", "resume file (relative to the resumes dir, or absolute)")
	f.StringVar(&ans.years, "years", "5", "total years of experience")
	f.StringVar(&ans.salary, "salary", "0", "desired salary (annual; monthly when --market brasil)")
	f.BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}

func (a *App) stdinIsTerminal() bool {
	f, ok := a.Stdin.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

func (a *App) wizard(ans *initAnswers) error {
	ans.market, ans.resume, ans.years, ans.salary, ans.acceptPacing = "internacional", "resume.pdf", "5", "0", true
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
				Description("Brazil: BRL, monthly salary. International: foreign currency, annual salary.").
				Options(huh.NewOption("Brasil", "brasil"), huh.NewOption("Internacional", "internacional")).
				Value(&ans.market),
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
				Value(&ans.acceptPacing),
		).Title("Applying"),
	).WithProgramOptions()
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

// applyAnswers folds the essentials into a default config.
func applyAnswers(cfg config.Config, ans initAnswers) (config.Config, error) {
	cfg.Personals.FirstName = strings.TrimSpace(ans.firstName)
	cfg.Personals.LastName = strings.TrimSpace(ans.lastName)
	cfg.Personals.Phone = strings.TrimSpace(ans.phone)
	cfg.Personals.City = strings.TrimSpace(ans.city)
	cfg.Personals.Country = strings.TrimSpace(ans.country)
	cfg.Questions.Resume = strings.TrimSpace(ans.resume)
	cfg.Questions.YearsOfExperience = strings.TrimSpace(ans.years)
	if err := cfg.Set("search.terms", ans.terms); err != nil {
		return cfg, err
	}
	cfg.Markets.Active = strings.TrimSpace(ans.market)
	salary, err := strconv.Atoi(strings.TrimSpace(ans.salary))
	if err != nil {
		return cfg, fmt.Errorf("--salary: %q is not a whole number", ans.salary)
	}
	switch cfg.Markets.Active {
	case "brasil":
		cfg.Markets.Brasil.DesiredSalary = salary
		if ans.location != "" {
			cfg.Markets.Brasil.Location = strings.TrimSpace(ans.location)
		}
	default:
		cfg.Questions.DesiredSalary = salary
		cfg.Search.Location = strings.TrimSpace(ans.location)
	}
	return cfg, nil
}
