// Package notify sends Telegram messages: the help-pause nudge, the login
// nudge, and (F1-7) the run summary and fatal errors. Ported from the Python
// modules/notify.py: off until both the token and the chat id are set, a
// half-configured pair warns once, and a failed send is a warning that never
// affects the run. The token is part of the request URL, so no URL is ever logged.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	client        *http.Client
	logger        *slog.Logger
	halfWarn      sync.Once
}

// New builds a notifier; token and chatID come from the environment or .env.
func New(token, chatID string, logger *slog.Logger) *Telegram {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Telegram{token: strings.TrimSpace(token), chatID: strings.TrimSpace(chatID), base: APIBase,
		client: &http.Client{Timeout: 10 * time.Second}, logger: logger}
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
				t.logger.Warn("Telegram notifications are off: set BOTH AJA_TELEGRAM_BOT_TOKEN and AJA_TELEGRAM_CHAT_ID to turn them on")
			})
		}
		return false
	}
	if r := []rune(text); len(r) > MaxMessageLength {
		text = string(r[:MaxMessageLength])
	}
	body, _ := json.Marshal(map[string]any{"chat_id": t.chatID, "text": text, "disable_web_page_preview": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		t.logger.Warn("could not build the Telegram request", "error", redact(err))
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil { // the run is ending; not a Telegram problem
			t.logger.Debug("Telegram notification abandoned", "error", ctx.Err())
			return false
		}
		t.logger.Warn("could not send the Telegram notification", "error", redact(err))
		return false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return true
	}
	var reply struct {
		Description string `json:"description"`
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	_ = json.Unmarshal(data, &reply)
	t.logger.Warn("Telegram rejected the notification", "status", res.StatusCode, "description", reply.Description)
	return false
}

// redact drops the request URL (which carries the token) from a client error.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
