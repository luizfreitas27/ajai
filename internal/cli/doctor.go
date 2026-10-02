package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/doctor"
)

func (a *App) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check Chrome, the profile, the config, secrets, resumes and the data directory",
		Long: `Runs every pre-flight check and prints the paths in use. Exit code 1 when any
check failed, 0 otherwise (warnings do not fail).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a.ui.Banner(a.Version)
			l, err := a.load()
			report := doctor.Run(l, err, a.paths, doctor.RealProbe())
			a.printReport(report)
			level := config.Defaults().Settings.LogLevel
			if l != nil {
				level = l.Config.Settings.LogLevel
			}
			a.openLog(level)
			a.logger.Info("doctor", "failed", report.Failed(), "checks", len(report.Checks))
			if report.Failed() {
				return &exitError{code: 1, msg: "doctor found problems"}
			}
			return nil
		},
	}
}

func (a *App) printReport(r doctor.Report) {
	for _, c := range r.Checks {
		switch c.Status {
		case doctor.OK:
			a.ui.OK(c.Name, c.Detail)
		case doctor.Warn:
			a.ui.Warn(c.Name, c.Detail)
		default:
			a.ui.Fail(c.Name, c.Detail)
		}
	}
	a.ui.Println()
	a.ui.Title("Paths")
	for _, pair := range r.Paths.All() {
		a.ui.KV(pair[0], pair[1])
	}
	a.ui.Println()
	a.ui.Title("Secret variables (environment or .env)")
	for _, name := range config.SecretEnvNames {
		a.ui.Hint(name)
	}
	fmt.Fprintln(a.Stdout)
}
