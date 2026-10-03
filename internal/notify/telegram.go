// Package notify talks to the user's Telegram bot: it sends the help-pause and
// login nudges, the run summary and fatal errors (ported from the Python
// modules/notify.py: off until both the token and the chat id are set, a
// half-configured pair warns once, a failed send is a warning that never
// affects the run), and carries the remote commands: it reads the messages and
// button presses they come in, and sends, edits and acknowledges the review
// queue's messages. The token is part of every request URL, built in one place
// (call), and no URL is ever logged or returned in an error.
package notify

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// MaxMessageLength is Telegram's limit per message, in characters.
const MaxMessageLength = 4096

// APIBase is the Bot API host; tests point it at a local server.
var APIBase = "https://api.telegram.org"

// Telegram sends messages to one chat. The zero value is a disabled notifier.
type Telegram struct {
	token, chatID string
	base          string
	client        *http.Client // sends: short timeout
	pollClient    *http.Client // long polls: bounded by the request's context
	logger        *slog.Logger
	halfWarn      sync.Once
}

// New builds a notifier; token and chatID come from the environment or .env.
func New(token, chatID string, logger *slog.Logger) *Telegram {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Telegram{token: strings.TrimSpace(token), chatID: strings.TrimSpace(chatID), base: APIBase,
		client: &http.Client{Timeout: 10 * time.Second}, pollClient: &http.Client{}, logger: logger}
}

// WithAPIBase points the notifier at another Bot API host (tests).
func (t *Telegram) WithAPIBase(base string) *Telegram {
	t.base = base
	return t
}

// Configured reports whether both settings are present, i.e. the user opted in.
func (t *Telegram) Configured() bool { return t != nil && t.token != "" && t.chatID != "" }

// Send posts text to the chat and reports whether Telegram accepted it. Every
// failure is one warning in the log and a false; nothing here can stop a run.
func (t *Telegram) Send(ctx context.Context, text string) bool {
	if t == nil {
		return false
	}
	if !t.Configured() {
		if t.token != "" || t.chatID != "" {
			t.halfWarn.Do(func() {
				t.logger.Warn("Telegram notifications are off: set BOTH AJAI_TELEGRAM_BOT_TOKEN and AJAI_TELEGRAM_CHAT_ID to turn them on")
			})
		}
		return false
	}
	err := t.call(ctx, t.client, "sendMessage", map[string]any{"chat_id": t.chatID, "text": truncate(text, MaxMessageLength), "disable_web_page_preview": true}, nil)
	var apiErr *APIError
	switch {
	case err == nil:
		return true
	case errors.As(err, &apiErr):
		t.logger.Warn("Telegram rejected the notification", "status", apiErr.Status, "description", apiErr.Description)
	case ctx.Err() != nil: // the run is ending; not a Telegram problem
		t.logger.Debug("Telegram notification abandoned", "error", ctx.Err())
	default:
		t.logger.Warn("could not send the Telegram notification", "error", err)
	}
	return false
}

// truncate cuts text to limit characters.
func truncate(text string, limit int) string {
	if r := []rune(text); len(r) > limit {
		return string(r[:limit])
	}
	return text
}

// redact drops the request URL (which carries the token) from a client error.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
