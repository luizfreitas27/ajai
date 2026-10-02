package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Update is one incoming Telegram update; only messages are read.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message is a chat message.
type Message struct {
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date"` // Unix seconds
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

// Time is when the message was sent.
func (m Message) Time() time.Time { return time.Unix(m.Date, 0) }

// Chat is where a message was sent.
type Chat struct {
	ID int64 `json:"id"`
}

// APIError is a request Telegram answered with ok:false.
type APIError struct {
	Status      int
	Description string
}

// Error is Telegram's own description, never the URL (it carries the token).
func (e *APIError) Error() string {
	return fmt.Sprintf("Telegram answered %d: %s", e.Status, e.Description)
}

// BotID is the bot's own id, the token's part before the colon (not secret).
func (t *Telegram) BotID() string {
	id, _, _ := strings.Cut(t.token, ":")
	return id
}

// ChatID is the configured chat, the only one aja talks to and listens to.
func (t *Telegram) ChatID() string { return t.chatID }

// Updates long-polls for updates from offset on, waiting up to wait for one to
// arrive. Errors never carry the request URL.
func (t *Telegram) Updates(ctx context.Context, offset int64, wait time.Duration) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, wait+15*time.Second)
	defer cancel()
	var updates []Update
	// Telegram remembers allowed_updates for later calls: add "callback_query" here
	// when buttons are read (/pending), not elsewhere.
	err := t.call(ctx, t.pollClient, "getUpdates", map[string]any{
		"offset": offset, "timeout": int(wait / time.Second), "allowed_updates": []string{"message"},
	}, &updates)
	return updates, err
}

// call posts a Bot API method and decodes its result.
func (t *Telegram) call(ctx context.Context, client *http.Client, method string, payload, result any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return redact(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return redact(err)
	}
	var reply struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(data, &reply); err != nil || !reply.OK {
		desc := reply.Description
		if desc == "" {
			desc = "an unreadable answer (" + strconv.Itoa(len(data)) + " bytes)"
		}
		return &APIError{Status: res.StatusCode, Description: desc}
	}
	if result != nil {
		return json.Unmarshal(reply.Result, result)
	}
	return nil
}
