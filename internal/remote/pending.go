package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/questions"
)

// pendingShown is how many entries one /pending shows: more would trip
// Telegram's flood limits and bury the chat.
const pendingShown = 10

// pending sends one message per pending remembered answer, newest first, with
// Approve and Delete buttons (Delete only when there is no answer yet). Each
// message is linked to its answer, so a reply to it reaches the answer even
// after a daemon restart.
func (l *Listener) pending(ctx context.Context) {
	entries, err := l.mem.List("pending")
	if err != nil {
		l.reply(ctx, "Could not read the answer memory: "+err.Error())
		return
	}
	if len(entries) == 0 {
		l.reply(ctx, "Nothing pending: every remembered answer is approved.")
		return
	}
	if err := l.store.ForgetTelegramMessagesOfGoneAnswers(); err != nil {
		l.logger.Warn("could not tidy the Telegram message links", "error", err)
	}
	shown := entries[:min(len(entries), pendingShown)]
	for _, e := range shown {
		id, err := l.bot.SendButtons(ctx, card(e, ""), buttons(e))
		if err != nil {
			l.logger.Warn("could not send a pending answer to Telegram", "error", err)
			l.reply(ctx, "Could not send the pending answers: "+err.Error())
			return
		}
		if err := l.store.LinkTelegramMessage(l.bot.ChatID(), id, e.ID); err != nil {
			l.logger.Warn("could not link a Telegram message to its answer; a reply to it will not be understood", "answer", e.ID, "error", err)
		}
	}
	if len(entries) > len(shown) {
		l.reply(ctx, fmt.Sprintf("Those are the %d newest of %d pending. Review them, then /pending shows the next.", len(shown), len(entries)))
	}
}

// sourceNames says who gave a remembered answer.
var sourceNames = map[string]string{"ai": "from the AI", "user": "yours", "form": "from the form"}

// card is the message showing one remembered answer; outcome, when given,
// replaces the hint at the bottom.
func card(e memory.Remembered, outcome string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question (%s): %s\n", e.Kind, e.Label)
	if len(e.Options) > 0 {
		fmt.Fprintf(&b, "Options: %s\n", strings.Join(e.Options, " · "))
	}
	switch {
	case strings.TrimSpace(e.Answer) == "":
		b.WriteString("Answer: none yet\n")
	case e.Kind == "checkbox":
		b.WriteString("Answer: tick it\n")
	default:
		fmt.Fprintf(&b, "Answer: %s (%s)\n", e.Answer, sourceNames[e.Source])
	}
	if e.LastJobLink != "" {
		fmt.Fprintf(&b, "Job: %s\n", e.LastJobLink)
	}
	fmt.Fprintf(&b, "Id: %s\n\n", e.ID)
	switch {
	case outcome != "":
		b.WriteString(outcome)
	case strings.TrimSpace(e.Answer) == "":
		b.WriteString("Reply to this message with the answer.")
	case e.Kind == "checkbox":
		b.WriteString("Approve to tick this box from now on.")
	default:
		b.WriteString("Approve keeps this answer; reply to this message to give another.")
	}
	return b.String()
}

func buttons(e memory.Remembered) []notify.Button {
	del := notify.Button{Text: "Delete", Data: "delete:" + e.ID}
	if strings.TrimSpace(e.Answer) == "" {
		return []notify.Button{del}
	}
	return []notify.Button{{Text: "Approve", Data: "approve:" + e.ID}, del}
}

// press acts on an Approve or Delete button and edits its message to show the
// outcome.
func (l *Listener) press(ctx context.Context, q *notify.CallbackQuery) {
	note := l.act(ctx, q)
	if err := l.bot.AnswerCallback(ctx, q.ID, note); err != nil {
		l.logger.Debug("could not acknowledge a Telegram button", "error", err)
	}
}

// act does what the button says and returns the note shown on the button.
func (l *Listener) act(ctx context.Context, q *notify.CallbackQuery) string {
	action, id, _ := strings.Cut(q.Data, ":")
	if action != "approve" && action != "delete" {
		return "aja does not know this button."
	}
	entry, err := l.mem.Get(id)
	if err != nil {
		return "Could not read the answer memory: " + err.Error()
	}
	if entry == nil {
		l.edit(ctx, q.Message.MessageID, q.Message.Text+"\n\nNo longer in the answer memory.")
		return "Already gone."
	}
	if action == "delete" {
		if err := l.mem.Delete(id); err != nil && !errors.Is(err, memory.ErrUnknownID) {
			return "Could not delete: " + err.Error()
		}
		l.edit(ctx, q.Message.MessageID, card(*entry, "Deleted: aja forgets this answer."))
		return "Deleted."
	}
	approved, err := l.mem.Approve(id, "")
	switch {
	case errors.Is(err, memory.ErrNothingToApprove):
		return "Reply to the message with the answer first."
	case err != nil:
		return "Could not approve: " + err.Error()
	}
	l.edit(ctx, q.Message.MessageID, card(*approved, "Approved: aja uses this answer from now on."))
	return "Approved."
}

// answer records a reply to a pending answer's message as its approved answer.
// For a select or radio the reply must name one of the remembered options.
func (l *Listener) answer(ctx context.Context, m notify.Message) {
	id, err := l.store.TelegramMessageAnswer(l.bot.ChatID(), m.ReplyTo.MessageID)
	if err != nil {
		l.reply(ctx, "Could not read the answer memory: "+err.Error())
		return
	}
	if id == "" {
		l.reply(ctx, "That message is not a pending answer; /pending lists them.")
		return
	}
	entry, err := l.mem.Get(id)
	switch {
	case err != nil:
		l.reply(ctx, "Could not read the answer memory: "+err.Error())
		return
	case entry == nil:
		l.reply(ctx, "That question is no longer in the answer memory.")
		return
	case entry.Kind == "checkbox":
		l.reply(ctx, "A checkbox takes no text: Approve ticks it from now on, Delete forgets it.")
		return
	}
	text := strings.TrimSpace(m.Text)
	if len(entry.Options) > 0 {
		i := pickOption(text, entry.Options)
		if i < 0 {
			l.reply(ctx, fmt.Sprintf("%q is not one of the options: %s. Reply again with one of them.", text, strings.Join(entry.Options, " · ")))
			return
		}
		text = entry.Options[i]
	}
	answered, err := l.mem.Approve(id, text)
	if err != nil {
		l.reply(ctx, "Could not save the answer: "+err.Error())
		return
	}
	if !l.edit(ctx, m.ReplyTo.MessageID, card(*answered, "Answered: aja uses this answer from now on.")) {
		l.reply(ctx, "Saved: "+text)
	}
}

// pickOption is the option text names: the same words ignoring case, else the
// option that honestly carries it (questions.MatchAnswerToOption); -1 if none.
func pickOption(text string, options []string) int {
	want := strings.Join(strings.Fields(text), " ")
	for i, o := range options {
		if strings.EqualFold(strings.Join(strings.Fields(o), " "), want) {
			return i
		}
	}
	return questions.MatchAnswerToOption(text, options)
}

// edit replaces a message's text and buttons; false when Telegram refused.
func (l *Listener) edit(ctx context.Context, messageID int64, text string) bool {
	if err := l.bot.Edit(ctx, messageID, text); err != nil {
		l.logger.Warn("could not edit a Telegram message", "error", err)
		return false
	}
	return true
}
