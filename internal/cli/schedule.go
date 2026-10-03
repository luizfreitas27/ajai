package cli

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

func (a *App) scheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Schedule windows: the weekdays and hours runs start and stop on their own",
		Long: `A schedule window is a span of local time on chosen weekdays, optionally with its own
market and dry run. When a window opens, one unattended run starts (a non-stop session when
settings.run_non_stop is on); when it closes, the run is stopped. A run that ends early is
not restarted in the same window, and a window that opens while another run is in
progress is skipped. Nothing starts on a window until ajai daemon runs.

Windows never cross midnight: split one in two (22:00-24:00 and 00:00-02:00 the next day).`,
	}
	cmd.AddCommand(a.scheduleAddCmd(), a.scheduleListCmd(), a.scheduleRmCmd(), a.scheduleEnableCmd(true), a.scheduleEnableCmd(false))
	// Changes to the schedule are logged, as `ajai import` logs.
	cmd.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		if err := c.Root().PersistentPreRunE(c, args); err != nil {
			return err
		}
		if c.Name() != "list" {
			a.openLog(config.Defaults().Settings.LogLevel)
		}
		return nil
	}
	return cmd
}

func (a *App) scheduleAddCmd() *cobra.Command {
	var days, start, end, market string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a schedule window",
		Example: `  ajai schedule add --days mon-fri --start 09:00 --end 11:00
  ajai schedule add --days sat --start 10:00 --end 12:00 --market internacional --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := schedule.Window{Market: market, DryRun: dryRun, Enabled: true}
			var err error
			if w.Days, err = schedule.ParseDays(days); err != nil {
				return fmt.Errorf("--days: %w", err)
			}
			if w.Start, err = schedule.ParseClock(start); err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			if w.End, err = schedule.ParseClock(end); err != nil {
				return fmt.Errorf("--end: %w", err)
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			if w.ID, err = st.AddValidWindow(w, config.MarketNameList()); err != nil {
				return err
			}
			a.logger.Info("schedule window added", "id", w.ID, "window", w.String())
			a.ui.OK("added", fmt.Sprintf("window %d: %s", w.ID, w))
			if windows, err := st.ListWindows(); err == nil { // the window is added either way
				a.printNextOpening(windows)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&days, "days", "", "weekdays: mon,wed,fri, a range such as mon-fri, weekdays, weekend or daily")
	f.StringVar(&start, "start", "", "start time, HH:MM (24-hour, local time)")
	f.StringVar(&end, "end", "", "end time, HH:MM; 24:00 for midnight")
	f.StringVar(&market, "market", "", "market for the window's runs: brasil or internacional (default: markets.active)")
	f.BoolVar(&dryRun, "dry-run", false, "the window's runs discard every form at Review instead of submitting")
	for _, name := range []string{"days", "start", "end"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

func (a *App) scheduleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the schedule windows and the next opening",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			windows, err := st.ListWindows()
			if err != nil {
				return err
			}
			if len(windows) == 0 {
				a.ui.Println("No schedule windows.")
				a.ui.Hint("Add one: ajai schedule add --days mon-fri --start 09:00 --end 11:00")
				return nil
			}
			for _, w := range windows {
				line := fmt.Sprintf("%3d  %s", w.ID, w)
				if !w.Enabled {
					line += "  (disabled)"
				}
				a.ui.Println(line)
			}
			a.ui.Println()
			a.printNextOpening(windows)
			return nil
		},
	}
}

func (a *App) scheduleRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"delete"},
		Short:   "Delete a schedule window",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, st, err := a.windowArg(args[0])
			if err != nil {
				return err
			}
			if _, err := st.DeleteWindow(w.ID); err != nil {
				return err
			}
			a.logger.Info("schedule window deleted", "id", w.ID, "window", w.String())
			a.ui.OK("deleted", fmt.Sprintf("window %d: %s", w.ID, w))
			return nil
		},
	}
}

// scheduleEnableCmd builds `ajai schedule enable` (on) and `ajai schedule disable`.
func (a *App) scheduleEnableCmd(on bool) *cobra.Command {
	use, short, action, verb := "disable <id>", "Turn a schedule window off, keeping it", "disable", "disabled"
	if on {
		use, short, action, verb = "enable <id>", "Turn a schedule window back on", "enable", "enabled"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, st, err := a.windowArg(args[0])
			if err != nil {
				return err
			}
			id := w.ID
			if w, err = st.SetWindowEnabledValid(id, on, config.MarketNameList()); err != nil {
				return fmt.Errorf("cannot %s window %d: %w", action, id, err)
			}
			a.logger.Info("schedule window "+verb, "id", w.ID, "window", w.String())
			a.ui.OK(verb, fmt.Sprintf("window %d: %s", w.ID, w))
			if windows, err := st.ListWindows(); err == nil { // the change is made either way
				a.printNextOpening(windows)
			}
			return nil
		},
	}
}

// windowArg reads a window id argument and returns that window and the store.
func (a *App) windowArg(arg string) (*schedule.Window, *store.Store, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id <= 0 {
		return nil, nil, fmt.Errorf("%q is not a window id; see ajai schedule list", arg)
	}
	st, err := a.openStore()
	if err != nil {
		return nil, nil, err
	}
	w, err := st.GetWindow(id)
	if err != nil {
		return nil, nil, err
	}
	if w == nil {
		return nil, nil, errors.New("no window " + arg + "; see ajai schedule list")
	}
	return w, st, nil
}

// printNextOpening says which window is open now, or when the next one opens.
func (a *App) printNextOpening(windows []schedule.Window) {
	now := a.now()
	if o, ok := schedule.Current(windows, now); ok {
		a.ui.KV("open now", fmt.Sprintf("window %d until %s (%s)", o.Window.ID, o.Window.End, o.Window))
	}
	if o, ok := schedule.Next(windows, now); ok {
		a.ui.KV("next opening", fmt.Sprintf("%s, window %d (%s)", o.Start.Format("Mon 02 Jan 15:04"), o.Window.ID, o.Window))
	} else {
		a.ui.KV("next opening", "none: every window is disabled")
	}
}

// now is the clock commands read; tests fix it with App.Now.
func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
