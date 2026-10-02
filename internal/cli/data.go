package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/importer"
	"github.com/luizfreitas27/aja/internal/store"
)

// openStore opens the database in the data directory and arranges for it to be closed.
func (a *App) openStore() (*store.Store, error) {
	if err := a.paths.EnsureDataDirs(); err != nil {
		return nil, err
	}
	st, err := store.Open(a.paths.DBFile)
	if err != nil {
		return nil, err
	}
	a.cleanups = append(a.cleanups, st.Close)
	return st, nil
}

func (a *App) importCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <python-checkout>",
		Short: "Bring settings, answer memory and job history over from the Python Auto_job_applier_linkedIn",
		Long: `Reads user_config.json into the config file (on top of the existing one, or the
defaults), answers_memory.json into the answer memory, the two spreadsheets in
"all excels" into the job history, and copies the resume into the resumes directory.
Secrets are never copied: the command lists the environment variables to set. Safe to
run more than once.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, lerr := config.Load(a.paths, config.MapEnv(nil), nil)
			if base == nil {
				return lerr
			}
			cfg := base.Config
			st, err := a.openStore()
			if err != nil {
				return err
			}
			rep, err := importer.Run(args[0], &cfg, st, a.paths)
			if err != nil {
				return err
			}
			if rep.ConfigWritten {
				if verr := cfg.Validate(); verr != nil {
					a.ui.Warn("config", "imported with problems; run `aja config validate`")
				}
				if err := config.Write(a.paths.ConfigFile, cfg); err != nil {
					return err
				}
			}
			a.openLog(cfg.Settings.LogLevel)
			a.logger.Info("import", "source", args[0], "answers", rep.Answers, "applied", rep.Applied, "external", rep.External, "failed", rep.Failed)
			a.printImportReport(rep)
			return nil
		},
	}
}

func (a *App) printImportReport(rep *importer.Report) {
	if rep.ConfigWritten {
		a.ui.OK("config", fmt.Sprintf("%d settings mapped into %s", len(rep.Mapped), a.paths.ConfigFile))
	} else {
		a.ui.Warn("config", "no user_config.json found; the config file was left alone")
	}
	a.ui.OK("answers", fmt.Sprintf("%d remembered answers", rep.Answers))
	a.ui.OK("jobs", fmt.Sprintf("%d applied, %d external, %d failed", rep.Applied, rep.External, rep.Failed))
	switch {
	case rep.ResumeCopied != "":
		a.ui.OK("resume", rep.ResumeCopied)
	case rep.ResumeMissing != "":
		a.ui.Warn("resume", rep.ResumeMissing+" was not found in the checkout; copy it to "+a.paths.ResumeDir+" by hand")
	}
	if len(rep.Unmapped) > 0 {
		a.ui.Println()
		a.ui.Title("Not imported")
		keys := make([]string, 0, len(rep.Unmapped))
		for k := range rep.Unmapped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			a.ui.KV(k, rep.Unmapped[k])
		}
	}
	if len(rep.SecretsSeen) > 0 {
		a.ui.Println()
		a.ui.Title("Secrets were NOT copied. Set these in " + a.paths.EnvFile + " or the environment:")
		for _, name := range rep.SecretsSeen {
			a.ui.Hint(name + "=...")
		}
	}
}

func (a *App) jobsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "jobs", Short: "Browse the job history"}
	var applied, external, failed, skipped, asJSON bool
	var since string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List jobs, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			f := store.JobFilter{Limit: limit}
			for flag, outcome := range map[*bool]store.Outcome{&applied: store.Applied, &external: store.External, &failed: store.Failed, &skipped: store.Skipped} {
				if *flag {
					f.Outcomes = append(f.Outcomes, outcome)
				}
			}
			if since != "" {
				t, err := time.ParseInLocation("2006-01-02", since, time.Local)
				if err != nil {
					return errors.New("--since takes a date as YYYY-MM-DD")
				}
				f.Since = t
			}
			jobs, err := st.ListJobs(f)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(a.Stdout)
				enc.SetIndent("", "  ")
				if jobs == nil {
					jobs = []store.Job{}
				}
				return enc.Encode(jobs)
			}
			if len(jobs) == 0 {
				a.ui.Println("No jobs recorded yet.")
				return nil
			}
			for _, j := range jobs {
				detail := strings.TrimSpace(j.Title + "  " + j.Company)
				if j.Reason != "" {
					detail += "  (" + j.Reason + ")"
				}
				a.ui.Printf("%-9s %-14s %-22s %s\n", string(j.Outcome), j.JobID, j.RecordedAt, detail)
			}
			return nil
		},
	}
	list.Flags().BoolVar(&applied, "applied", false, "only applied jobs")
	list.Flags().BoolVar(&external, "external", false, "only external jobs")
	list.Flags().BoolVar(&failed, "failed", false, "only failed jobs")
	list.Flags().BoolVar(&skipped, "skipped", false, "only skipped jobs")
	list.Flags().StringVar(&since, "since", "", "only jobs recorded on or after this date (YYYY-MM-DD)")
	list.Flags().IntVar(&limit, "limit", 0, "at most this many rows (0 = all)")
	list.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(list)
	return cmd
}

func (a *App) exportCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the job history as the Python bot's spreadsheets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				return errors.New("--csv <dir> is required")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			applied, failed, err := st.ExportCSV(dir)
			if err != nil {
				return err
			}
			a.ui.OK(store.AppliedCSVName, fmt.Sprintf("%d rows (applied and external)", applied))
			a.ui.OK(store.FailedCSVName, fmt.Sprintf("%d rows", failed))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "csv", "", "directory to write the two CSV files into")
	return cmd
}
