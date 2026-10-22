package cli

import (
	"github.com/spf13/cobra"

	"github.com/luizfreitas27/ajai/internal/web"
)

func (a *App) serveCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the local web API and front on a loopback address",
		Long: `Serves the web front's API (and the front itself, when built) on a loopback address,
until Ctrl-C. Nothing is authenticated: anyone on this machine who can reach the port acts
as you, so it listens on 127.0.0.1 (or ::1, localhost) only, and refuses a request whose
Host or Origin is not loopback. Runs it starts are ajai run --unattended --origin web, in
processes of their own, as the TUI starts its runs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := web.ValidAddr(addr); err != nil {
				return err
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			l, _ := a.load()
			a.openLog(logLevel(l))
			self, err := executable()
			if err != nil {
				return err
			}
			h := web.New(web.Deps{DataDir: a.paths.DataDir, LogDir: a.paths.LogDir, Paths: a.paths, Env: a.Env, Store: st, Logger: a.logger,
				Launcher: launcher{executable: self, configFlag: a.configFlag, noColor: a.noColor}})
			a.ui.KV("serving", "http://"+addr+" (Ctrl-C stops it)")
			return web.Serve(cmd.Context(), addr, h)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7077", "the loopback address to listen on")
	return cmd
}
