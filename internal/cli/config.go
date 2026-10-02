package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/config"
)

func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, read, write and validate the configuration",
	}
	cmd.AddCommand(a.configShowCmd(), a.configGetCmd(), a.configSetCmd(), a.configValidateCmd(), a.configPathCmd())
	return cmd
}

func (a *App) configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration as YAML, secrets masked",
		Long: `Prints the configuration after every layer (defaults, file, AJA_CFG_ environment
overrides, .env) and lists which keys were overridden. Secrets are shown only as
present or absent.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := a.load()
			if l == nil {
				return err
			}
			data, merr := config.Marshal(l.Config)
			if merr != nil {
				return merr
			}
			a.ui.Printf("%s", data)
			a.ui.Println()
			a.ui.Title("Secrets (from the environment or " + a.paths.EnvFile + ")")
			for _, nv := range l.Secrets.Values() {
				a.ui.KV(nv[0], presence(nv[1]))
			}
			if len(l.Overrides) > 0 {
				a.ui.Println()
				a.ui.Title("Overridden by the environment")
				for _, k := range l.Overrides {
					v, _ := l.Config.Get(k)
					a.ui.KV(k, v)
				}
			}
			if err != nil {
				a.ui.Println()
				a.ui.Error(err)
				return &exitError{code: 1, msg: "the configuration has problems (see above)"}
			}
			return nil
		},
	}
}

func presence(secret string) string {
	if secret == "" {
		return "not set"
	}
	return config.Mask + " (set)"
}

func (a *App) configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <section.key>",
		Short: "Print one value (lists are comma-separated)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := a.load()
			if l == nil {
				return err
			}
			v, err := l.Config.Get(args[0])
			if err != nil {
				return err
			}
			a.ui.Println(v)
			return nil
		},
	}
}

func (a *App) configSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <section.key> <value>",
		Short: "Write one value to the config file, after validating it",
		Long: `Sets a key and rewrites the file with its comments. Lists take a comma-separated
value; an empty string ("") clears a list. The whole file is validated first and the
write is refused, naming the key and the valid values, when the new value is wrong.
Environment overrides are not consulted: the file is what changes.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The file alone, with no environment layer, so an override is never persisted.
			l, err := config.Load(a.paths, config.MapEnv(nil), nil)
			if l == nil {
				return err
			}
			if !l.FileExisted {
				return errors.New("no config file at " + a.paths.ConfigFile + "; run `aja init` first")
			}
			key, value := args[0], args[1]
			other, err := l.Config.SetChecked(key, value)
			if err != nil {
				return fmt.Errorf("refusing to write: %w", err)
			}
			if other != nil {
				a.ui.Warn("config", fmt.Sprintf("the file still has %d other problem(s); run `aja config validate`", len(other.Errors)))
			}
			if err := config.Write(a.paths.ConfigFile, l.Config); err != nil {
				return err
			}
			a.openLog(l.Config.Settings.LogLevel)
			got, _ := l.Config.Get(key)
			a.logger.Info("config set", "key", key, "value", got)
			a.ui.OK(key, got)
			return nil
		},
	}
	// Values such as -3 are arguments, not flags, once the two positionals start.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func (a *App) configValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Check the configuration and report every problem",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := a.load()
			if l == nil {
				return err
			}
			if !l.FileExisted {
				a.ui.Warn("config", "no config file at "+a.paths.ConfigFile+"; validating the built-in defaults")
			}
			if err != nil {
				if ve, ok := config.AsValidation(err); ok {
					for _, fe := range ve.Errors {
						a.ui.Fail(fe.Key, strings.TrimPrefix(fe.Error(), fe.Key+": "))
					}
					return &exitError{code: 1, msg: fmt.Sprintf("%d problem(s) in %s", len(ve.Errors), a.paths.ConfigFile)}
				}
				return err
			}
			a.ui.OK("config", a.paths.ConfigFile+" is valid")
			return nil
		},
	}
}

func (a *App) configPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { a.ui.Println(a.paths.ConfigFile) },
	}
}
