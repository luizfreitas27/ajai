package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/launch"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/run"
)

func (a *App) loginCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to LinkedIn once, in the Chrome profile ajai uses",
		Long: `Opens Chrome on the profile ajai uses (the ajai profile, unless settings.chrome_profile_dir
names another) at LinkedIn and waits until you have signed in, then closes it. Do this once before
the first run: ajai never types your password, and runs started by the scheduler have nobody
to sign in for them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Only the Chrome settings matter here, so a config that does not validate
			// yet (or does not exist) is no reason to refuse.
			l, err := a.load()
			if l == nil {
				return err
			}
			level := config.Defaults().Settings.LogLevel
			if err == nil {
				level = l.Config.Settings.LogLevel
			}
			a.openLog(level)
			if holder, held, err := run.ReadLock(a.paths.DataDir); err != nil {
				return err
			} else if held {
				return fmt.Errorf("%s is using Chrome; sign in after it ends, or stop it with `ajai stop`", holder)
			}
			if err := a.paths.EnsureDataDirs(); err != nil {
				return err
			}
			ctx := cmd.Context()
			// Signing in is by hand, so the window is always shown, whatever settings.window says.
			cfg := l.Config
			cfg.Settings.Window = string(browser.WindowVisible)
			br, profile, err := launch.OpenChrome(ctx, cfg, a.paths, OpenBrowser, a.logger)
			if err != nil {
				return err
			}
			defer func() { _ = br.Close() }()
			waited := false
			err = linkedin.NewSession(br.Main(), br, nil, a.logger).EnsureLoggedIn(ctx, timeout, func() {
				waited = true
				a.ui.Warn("login", fmt.Sprintf("Sign in to LinkedIn in the Chrome window (up to %s); ajai continues when the feed appears.", timeout))
			})
			switch {
			case errors.Is(err, linkedin.ErrLoginTimeout):
				return fmt.Errorf("no LinkedIn sign-in within %s; run `ajai login` again when you have time to sign in", timeout)
			case err != nil:
				return err
			case waited:
				a.ui.OK("signed in", "LinkedIn session saved in "+profile.Dir)
			default:
				a.ui.OK("signed in", "already signed in to LinkedIn in "+profile.Dir)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", run.DefaultLoginTimeout, "how long to wait for you to sign in")
	return cmd
}
