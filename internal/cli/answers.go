package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
them. Only an approved answer may tick a checkbox.

Questions nothing could answer wait here too, with no answer yet: give them one with
"edit" (or in "review") and the next form that asks goes through.`,
	}
	cmd.AddCommand(a.answersListCmd(), a.answersReviewCmd(), a.answersApproveCmd(), a.answersEditCmd(), a.answersDeleteCmd(), a.answersClearCmd())
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

func (a *App) answersReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "Walk through the pending answers one by one: approve, edit, delete or skip",
		Long: `Shows each pending answer with where it came from, and asks what to do:
  a  approve it as it is
  e  type a corrected answer (it becomes yours, approved)
  d  delete it
  s  skip it for now (also Enter)
  q  stop reviewing
Questions aja could not answer at all are here too, with no answer yet: type one with e.
For a checkbox, the answer "checked" lets aja tick that box from now on.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := a.openMemory()
			if err != nil {
				return err
			}
			pending, err := mem.List("pending")
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				a.ui.Println("Nothing to review.")
				return nil
			}
			ctx := cmd.Context()
			a.ui.Title(fmt.Sprintf("%d pending answer(s)", len(pending)))
			var approved, edited, deleted, skipped int
		entries:
			for i, e := range pending {
				a.ui.Println()
				a.ui.Printf("[%d/%d] %s\n", i+1, len(pending), e.Label)
				where := fmt.Sprintf("%s, from %s, used %d time(s)", e.Kind, e.Source, e.Uses)
				if e.LastJobLink != "" {
					where += ", last on " + e.LastJobLink
				}
				a.ui.Hint(where)
				if e.Answer == "" {
					a.ui.Hint("answer: (none yet)")
				} else {
					a.ui.Hint(fmt.Sprintf("answer: %q", e.Answer))
				}
				for {
					a.ui.Printf("  [a]pprove [e]dit [d]elete [s]kip [q]uit > ")
					choice, err := a.readLine(ctx, 0)
					if errors.Is(err, io.EOF) {
						break entries
					}
					if err != nil {
						return err
					}
					switch strings.ToLower(strings.TrimSpace(choice)) {
					case "a", "approve":
						_, err := mem.Approve(e.ID, "")
						if errors.Is(err, memory.ErrNothingToApprove) {
							a.ui.Warn("approve", "there is no answer to approve yet; use e to write one")
							continue
						}
						if gone(err) {
							a.ui.Warn("gone", "this answer was deleted elsewhere; moving on")
							break
						}
						if err != nil {
							return err
						}
						approved++
					case "e", "edit":
						a.ui.Printf("  new answer > ")
						text, err := a.readLine(ctx, 0)
						if err != nil && !errors.Is(err, io.EOF) {
							return err
						}
						if strings.TrimSpace(text) == "" {
							a.ui.Hint("nothing typed; unchanged")
							continue
						}
						if _, err := mem.Approve(e.ID, strings.TrimSpace(text)); gone(err) {
							a.ui.Warn("gone", "this answer was deleted elsewhere; moving on")
							break
						} else if err != nil {
							return err
						}
						edited++
					case "d", "delete":
						if err := mem.Delete(e.ID); err != nil && !gone(err) {
							return err
						}
						deleted++
					case "", "s", "skip":
						skipped++
					case "q", "quit":
						break entries
					default:
						a.ui.Hint("type a, e, d, s or q")
						continue
					}
					break
				}
			}
			left, err := mem.PendingCount()
			if err != nil {
				return err
			}
			a.logger.Info("answers reviewed", "approved", approved, "edited", edited, "deleted", deleted, "skipped", skipped)
			a.ui.Println()
			a.ui.OK("reviewed", fmt.Sprintf("%d approved, %d edited, %d deleted, %d skipped; %d still pending", approved, edited, deleted, skipped, left))
			return nil
		},
	}
}

// gone reports an entry deleted by another process while it was being reviewed.
func gone(err error) bool { return errors.Is(err, memory.ErrUnknownID) }
