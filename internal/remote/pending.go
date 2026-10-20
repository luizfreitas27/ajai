package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/memory"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/notify"
	"github.com/luizfreitas27/ajai/internal/questions"
	"github.com/luizfreitas27/ajai/internal/store"
)

const (
	// pendingShown is how many pending answers one /pending shows: more would
	// trip Telegram's flood limits and bury the chat.
	pendingShown = 10
	// linkMaxAge is how long a reply to a /pending message still reaches its
	// answer.
	linkMaxAge = 30 * 24 * time.Hour
	// unreadable starts the reply when the answer memory cannot be read.
	unreadable = "Could not read the answer memory: "
)

// pending sends one message per pending remembered answer, newest first, with
// Approve and Delete buttons (Delete only when there is no answer yet). Each
// message is linked to its answer, so a reply to it reaches the answer even
// after a daemon restart.
func (l *Listener) pending(ctx context.Context) {
	answers, err := l.mem.List("pending")
	if err != nil {
		l.reply(ctx, unreadable+err.Error())
		return
	}
	if len(answers) == 0 {
		l.reply(ctx, "Nothing pending: every remembered answer is approved.")
		return
	}
	if err := l.store.ForgetTelegramMessages(time.Now().Add(-linkMaxAge)); err != nil {
		l.logger.Warn("could not forget old Telegram message links", "error", err)
	}
	shown := answers[:min(len(answers), pendingShown)]
	for _, a := range shown {
		id, err := l.sendButtons(ctx, describe(a, ""), buttons(a))
		if err != nil {
			l.logger.Warn("could not send a pending answer to Telegram", "error", err)
			l.reply(ctx, "Could not send the pending answers: "+err.Error())
			return
		}
		if err := l.store.LinkTelegramMessage(l.message(id), a.ID); err != nil {
			l.logger.Warn("could not link a Telegram message to its answer; a reply to it will not be understood", "answer", a.ID, "error", err)
		}
	}
	if len(answers) > len(shown) {
		l.reply(ctx, fmt.Sprintf("Those are the %d newest of %d pending. Review them, then /pending shows the next.", len(shown), len(answers)))
	}
}

// sendButtons sends a message with buttons; when Telegram says too many were
// sent, it waits as long as Telegram asks (up to a minute) and tries once more.
func (l *Listener) sendButtons(ctx context.Context, text string, b []notify.Button) (int64, error) {
	id, err := l.bot.SendButtons(ctx, text, b)
	var apiErr *notify.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests && apiErr.RetryAfter <= time.Minute {
		if l.sleep(ctx, apiErr.RetryAfter) != nil {
			return 0, err
		}
		return l.bot.SendButtons(ctx, text, b)
	}
	return id, err
}

// message names a message the bot sent to the configured chat.
func (l *Listener) message(id int64) store.TelegramMessage {
	return store.TelegramMessage{Bot: l.bot.BotID(), Chat: l.bot.ChatID(), ID: id}
}

// sourceNames says who gave a remembered answer.
var sourceNames = map[string]string{"ai": "from the AI", "user": "yours", "form": "from the form"}

func unanswered(a memory.Remembered) bool { return strings.TrimSpace(a.Answer) == "" }
func checkbox(a memory.Remembered) bool   { return a.Kind == string(questions.Checkbox) }

// describe is the message showing one remembered answer; outcome, when given,
// replaces the hint at the bottom.
func describe(a memory.Remembered, outcome string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question (%s): %s\n", a.Kind, a.Label)
	if len(a.Options) > 0 {
		fmt.Fprintf(&b, "Options: %s\n", strings.Join(a.Options, " · "))
	}
	hint := "Approve keeps this answer; reply to this message to give another."
	switch {
	case unanswered(a):
		b.WriteString("Answer: none yet\n")
		hint = "Reply to this message with the answer."
	case checkbox(a):
		b.WriteString("Answer: tick it\n")
		hint = "Approve to tick this box from now on."
	default:
		fmt.Fprintf(&b, "Answer: %s (%s)\n", a.Answer, sourceNames[a.Source])
	}
	if a.LastJobLink != "" {
		fmt.Fprintf(&b, "Job: %s\n", a.LastJobLink)
	}
	fmt.Fprintf(&b, "Id: %s\n\n", a.ID)
	if outcome != "" {
		hint = outcome
	}
	b.WriteString(hint)
	return b.String()
}

// buttons are Approve and Delete, or Delete alone for a question with no answer
// yet. Approve carries a digest of the answer shown, so it never approves an
// answer a run gave after the message was sent.
func buttons(a memory.Remembered) []notify.Button {
	del := notify.Button{Text: "Delete", Data: "delete:" + a.ID}
	if unanswered(a) {
		return []notify.Button{del}
	}
	return []notify.Button{{Text: "Approve", Data: "approve:" + a.ID + ":" + messages.Digest(a.Answer)}, del}
}

// pressed acts on an Approve or Delete button and acknowledges it.
func (l *Listener) pressed(ctx context.Context, q *notify.CallbackQuery) {
	note := l.applyButton(ctx, q)
	if err := l.bot.AnswerCallback(ctx, q.ID, note); err != nil {
		l.logger.Debug("could not acknowledge a Telegram button", "error", err)
	}
}

// applyButton does what the button says, edits its message to show the
// outcome, and returns the note shown on the button.
func (l *Listener) applyButton(ctx context.Context, q *notify.CallbackQuery) string {
	action, rest, _ := strings.Cut(q.Data, ":")
	id, shown, _ := strings.Cut(rest, ":")
	if action != "delete" && (action != "approve" || shown == "") {
		return "ajai does not know this button."
	}
	a, err := l.mem.Get(id)
	if err != nil {
		return unreadable + err.Error()
	}
	if a == nil {
		l.edit(ctx, q.Message.MessageID, q.Message.Text+"\n\nNo longer in the answer memory.", nil)
		return "Already gone."
	}
	if action == "delete" {
		if err := l.mem.Delete(id); err != nil && !errors.Is(err, memory.ErrUnknownID) {
			return "Could not delete: " + err.Error()
		}
		l.edit(ctx, q.Message.MessageID, describe(*a, "Deleted: ajai forgets this answer."), nil)
		return "Deleted."
	}
	if shown != messages.Digest(a.Answer) {
		l.edit(ctx, q.Message.MessageID, describe(*a, ""), buttons(*a))
		return "The answer changed since this message; check it again."
	}
	approved, err := l.mem.Approve(id, "")
	switch {
	case errors.Is(err, memory.ErrNothingToApprove):
		return "Reply to the message with the answer first."
	case err != nil:
		return "Could not approve: " + err.Error()
	}
	l.edit(ctx, q.Message.MessageID, describe(*approved, "Approved: ajai uses this answer from now on."), nil)
	return "Approved."
}

// replied records a reply to a pending answer's message as its approved
// answer. For a select or radio the reply must name one of the remembered
// options.
func (l *Listener) replied(ctx context.Context, m notify.Message) {
	id, err := l.store.TelegramMessageAnswer(l.message(m.ReplyTo.MessageID))
	if err != nil {
		l.reply(ctx, unreadable+err.Error())
		return
	}
	if id == "" {
		l.reply(ctx, "That message is not a pending answer; /pending lists them.")
		return
	}
	a, err := l.mem.Get(id)
	switch {
	case err != nil:
		l.reply(ctx, unreadable+err.Error())
		return
	case a == nil:
		l.reply(ctx, "That question is no longer in the answer memory.")
		return
	case checkbox(*a):
		l.reply(ctx, "A checkbox takes no text: Approve ticks it from now on, Delete forgets it.")
		return
	}
	text := strings.TrimSpace(m.Text)
	if len(a.Options) > 0 {
		i := questions.PickOption(text, a.Options)
		if i < 0 {
			l.reply(ctx, fmt.Sprintf("%q is not one of the options: %s. Reply again with one of them, or its beginning.", text, strings.Join(a.Options, " · ")))
			return
		}
		text = a.Options[i]
	}
	answered, err := l.mem.Approve(id, text)
	if err != nil {
		l.reply(ctx, "Could not save the answer: "+err.Error())
		return
	}
	if !l.edit(ctx, m.ReplyTo.MessageID, describe(*answered, "Answered: ajai uses this answer from now on."), nil) {
		l.reply(ctx, "Saved: "+text)
	}
}

// edit replaces a message's text and buttons (none removes them); false when
// Telegram refused.
func (l *Listener) edit(ctx context.Context, messageID int64, text string, b []notify.Button) bool {
	if err := l.bot.Edit(ctx, messageID, text, b); err != nil {
		l.logger.Warn("could not edit a Telegram message", "error", err)
		return false
	}
	return true
}
