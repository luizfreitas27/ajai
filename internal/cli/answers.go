package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/memory"
)

func (a *App) openMemory() (*memory.Memory, error) {
	st, err := a.openStore()
	if err != nil {
		return nil, err
	}
	return memory.New(st, a.logger), nil
}

func (a *App) answersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "answers",
		Short: "The answer memory: review what the bot answered to unrecognised questions",
		Long: `Every answer given to a question the configuration does not cover is remembered
with a review state. Pending answers (from the AI, or a checkbox the bot saw and left
alone) are still used; approving confirms them, editing corrects them, deleting forgets
them. Only an approved answer may tick a checkbox.`,
	}
	cmd.AddCommand(a.answersListCmd(), a.answersApproveCmd(), a.answersEditCmd(), a.answersDeleteCmd(), a.answersClearCmd())
	return cmd
}

func (a *App) answersListCmd() *cobra.Command {
	var state string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List remembered answers, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if state != "" && !slices.Contains(memory.States, state) {
				return errors.New("--state takes " + strings.Join(memory.States, " or "))
			}
			mem, err := a.openMemory()
			if err != nil {
				return err
			}
			entries, err := mem.List(state)
			if err != nil {
				return err
			}
			if asJSON {
				if entries == nil {
					entries = []memory.Remembered{}
				}
				enc := json.NewEncoder(a.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
			}
			if len(entries) == 0 {
				if state == "" {
					a.ui.Println("No remembered answers.")
				} else {
					a.ui.Println("No remembered answers in state " + state + ".")
				}
				return nil
			}
			for _, e := range entries {
				a.ui.Printf("%s  %-8s %-9s %-5s  %s\n", e.ID, e.State, e.Kind, e.Source, e.Label)
				a.ui.Hint(fmt.Sprintf("answer: %q  uses: %d", e.Answer, e.Uses))
			}
			pending, err := mem.PendingCount()
			if err != nil {
				return err
			}
			a.ui.Println()
			a.ui.Printf("%d pending\n", pending)
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "pending or approved (default: all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func (a *App) answersApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve <id>",
		Short: "Confirm a remembered answer as-is",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := a.openMemory()
			if err != nil {
				return err
			}
			entry, err := mem.Approve(args[0], "")
			if err != nil {
				return err
			}
			a.logger.Info("answer approved", "id", entry.ID, "question", entry.Label)
			a.ui.OK("approved", fmt.Sprintf("%q -> %q", entry.Label, entry.Answer))
			return nil
		},
	}
}

func (a *App) answersEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <id> <answer>",
		Short: "Correct a remembered answer; the correction is yours and counts as approved",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			answer := strings.TrimSpace(args[1])
			if answer == "" {
				return errors.New("the new answer must not be blank; use delete to forget it")
			}
			mem, err := a.openMemory()
			if err != nil {
				return err
			}
			entry, err := mem.Approve(args[0], answer)
			if err != nil {
				return err
			}
			a.logger.Info("answer edited", "id", entry.ID, "question", entry.Label)
			a.ui.OK("approved", fmt.Sprintf("%q -> %q", entry.Label, entry.Answer))
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func (a *App) answersDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Forget a remembered answer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := a.openMemory()
			if err != nil {
				return err
			}
			if err := mem.Delete(args[0]); err != nil {
				return err
			}
			a.logger.Info("answer deleted", "id", args[0])
			a.ui.OK("deleted", args[0])
			return nil
		},
	}
}

func (a *App) answersClearCmd() *cobra.Command {
	var state string
	cmd := &cobra.Command{
		Use:   "clear --state pending|approved",
		Short: "Forget every remembered answer in one review state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !slices.Contains(memory.States, state) {
				return errors.New("--state is required and takes " + strings.Join(memory.States, " or "))
			}
			mem, err := a.openMemory()
			if err != nil {
				return err
			}
			n, err := mem.DeleteAll(state)
			if err != nil {
				return err
			}
			a.logger.Info("answers cleared", "state", state, "count", n)
			a.ui.OK("cleared", fmt.Sprintf("%d %s answer(s)", n, state))
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "which review state to clear")
	return cmd
}
