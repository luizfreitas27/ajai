package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/store"
)

// messageStates are the states "messages list --state" takes, in the order a
// message moves through them.
var messageStates = []string{store.MessageQueued, store.MessageApproved, store.MessageSending, store.MessageSent, store.MessageFailed, store.MessageNoRecipient}

// messageRow is one recruiter message as list shows it, with the job it was
// written for.
type messageRow struct {
	ID            int64  `json:"id"`
	State         string `json:"state"`
	JobID         string `json:"job_id"`
	Title         string `json:"title"`
	Company       string `json:"company"`
	RecipientName string `json:"recipient_name"`
	RecipientURL  string `json:"recipient_url"`
	Text          string `json:"text"`
	Digest        string `json:"digest"`
}

func (a *App) messagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "messages",
		Short: "Recruiter messages: review the queue, approve what may be sent, delete what may not",
		Long: `After a submitted application, ajai may queue one message to a hirer of that job.
Nothing is sent until you approve it. Approve a message by the digest of the text you
read, so an edited text is refused; approve the whole queue by the list digest that
"list" prints, so a queue that changed since is refused too.`,
	}
	cmd.AddCommand(a.messagesListCmd(), a.messagesApproveCmd(), a.messagesDeleteCmd())
	return cmd
}

// messageRows returns the messages in state ("" for all), newest first.
func (a *App) messageRows(st *store.Store, state string) ([]messageRow, error) {
	msgs, err := st.ListMessages(state)
	if err != nil {
		return nil, err
	}
	jobs := map[string]*store.Job{}
	rows := make([]messageRow, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		job, seen := jobs[m.JobID]
		if !seen {
			if job, err = st.GetJob(m.JobID); err != nil {
				return nil, err
			}
			jobs[m.JobID] = job
		}
		row := messageRow{ID: m.ID, State: m.State, JobID: m.JobID, RecipientName: m.RecipientName,
			RecipientURL: m.RecipientURL, Text: m.Text, Digest: messages.Digest(m.Text)}
		if job != nil {
			row.Title, row.Company = job.Title, job.Company
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (a *App) messagesListCmd() *cobra.Command {
	var state string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recruiter messages, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if state != "" && !slices.Contains(messageStates, state) {
				return errors.New("--state takes " + strings.Join(messageStates, ", ") + " or no_recipient")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			rows, err := a.messageRows(st, state)
			if err != nil {
				return err
			}
			if asJSON {
				if rows == nil {
					rows = []messageRow{}
				}
				enc := json.NewEncoder(a.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			if len(rows) == 0 {
				if state == "" {
					a.ui.Println("No recruiter messages.")
				} else {
					a.ui.Println("No recruiter messages in state " + state + ".")
				}
				return nil
			}
			var queued []messages.Item
			for _, r := range rows {
				a.ui.Printf("%d  %-11s %s | %s\n", r.ID, r.State, r.Title, r.Company)
				a.ui.Hint(fmt.Sprintf("to %s  %s", r.RecipientName, r.RecipientURL))
				a.ui.Hint("text: " + r.Text)
				a.ui.Hint("digest: " + r.Digest)
				if r.State == store.MessageQueued {
					queued = append(queued, messages.Item{ID: r.ID, Digest: r.Digest})
				}
			}
			if len(queued) > 0 {
				a.ui.Println()
				a.ui.Printf("%d queued. To approve them all, as listed:\n", len(queued))
				a.ui.Hint("ajai messages approve --all " + messages.ListDigest(queued))
				a.ui.Hint("or one at a time:")
				for _, it := range queued {
					a.ui.Hint(fmt.Sprintf("ajai messages approve %d %s", it.ID, it.Digest))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "one state: "+strings.Join(messageStates, ", ")+" (default: all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func (a *App) messagesApproveCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "approve <id> <digest>",
		Short: "Approve a queued message as shown, or every queued message the last list showed",
		Long: `Approves the message with <id> when <digest> is the digest of the text it has now,
the text you read. With --all, the argument is the list digest that "list" printed:
every queued message is approved only if the queue is still the one that list showed.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.ExactArgs(1)(cmd, args)
			}
			return cobra.ExactArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			if all {
				n, err := st.ApproveAll(args[0])
				if errors.Is(err, store.ErrQueueChanged) {
					return errors.New("the queue changed since you listed it; run ajai messages list again. Nothing was approved")
				}
				if err != nil {
					return err
				}
				a.logger.Info("messages approved", "count", n)
				a.ui.OK("approved", fmt.Sprintf("%d message(s)", n))
				return nil
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("the message id must be a number, not %q", args[0])
			}
			switch err := st.ApproveMessage(id, args[1]); {
			case errors.Is(err, store.ErrNoMessage):
				return fmt.Errorf("there is no message %d; list shows the ids", id)
			case errors.Is(err, store.ErrNotQueued):
				return fmt.Errorf("message %d is not queued, so it cannot be approved; list shows its state", id)
			case errors.Is(err, store.ErrTextChanged):
				return fmt.Errorf("message %d changed since it was shown; run ajai messages list again", id)
			case err != nil:
				return err
			}
			a.logger.Info("message approved", "id", id)
			a.ui.OK("approved", fmt.Sprintf("message %d", id))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "approve every queued message the last list showed; the argument is that list's digest")
	return cmd
}

func (a *App) messagesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a recruiter message; a message being sent cannot be deleted",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("the message id must be a number, not %q", args[0])
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			m, err := st.GetMessage(id)
			if err != nil {
				return err
			}
			if m == nil {
				return fmt.Errorf("there is no message %d; list shows the ids", id)
			}
			ok, err := st.DeleteMessage(id)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("message %d is being sent, so it cannot be deleted", id)
			}
			a.logger.Info("message deleted", "id", id)
			a.ui.OK("deleted", fmt.Sprintf("message %d", id))
			return nil
		},
	}
}
