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

// Update is one incoming Telegram update: a message, or a button pressed.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Message is a chat message.
type Message struct {
	MessageID int64    `json:"message_id"`
	Date      int64    `json:"date"` // Unix seconds
	Chat      Chat     `json:"chat"`
	Text      string   `json:"text"`
	ReplyTo   *Message `json:"reply_to_message"` // the message this one replies to
}

// CallbackQuery is an inline button pressed under one of the bot's messages.
type CallbackQuery struct {
	ID      string   `json:"id"`
	Message *Message `json:"message"` // the message the button is on
	Data    string   `json:"data"`    // the pressed Button's Data
}

// Button is an inline button under a message; its Data comes back in the
// CallbackQuery when it is pressed (at most 64 bytes).
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
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
	RetryAfter  time.Duration // 429: how long Telegram asks to wait
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

// ChatID is the configured chat, the only one ajai talks to and listens to.
func (t *Telegram) ChatID() string { return t.chatID }

// Updates long-polls for updates from offset on, waiting up to wait for one to
// arrive. Errors never carry the request URL.
func (t *Telegram) Updates(ctx context.Context, offset int64, wait time.Duration) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, wait+15*time.Second)
	defer cancel()
	var updates []Update
	err := t.call(ctx, t.pollClient, "getUpdates", map[string]any{
		"offset": offset, "timeout": int(wait / time.Second), "allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	return updates, err
}

// SendButtons posts text with one row of inline buttons and returns the new
// message's id. Unlike Send, a failure is returned, not logged.
func (t *Telegram) SendButtons(ctx context.Context, text string, buttons []Button) (int64, error) {
	var sent Message
	err := t.call(ctx, t.client, "sendMessage", map[string]any{"chat_id": t.chatID, "text": truncate(text, MaxMessageLength), "disable_web_page_preview": true,
		"reply_markup": map[string]any{"inline_keyboard": [][]Button{buttons}}}, &sent)
	return sent.MessageID, err
}

// Edit replaces the text and buttons of one of the bot's messages in the chat;
// no buttons removes them.
func (t *Telegram) Edit(ctx context.Context, messageID int64, text string, buttons []Button) error {
	rows := [][]Button{}
	if len(buttons) > 0 {
		rows = append(rows, buttons)
	}
	return t.call(ctx, t.client, "editMessageText", map[string]any{"chat_id": t.chatID, "message_id": messageID, "text": truncate(text, MaxMessageLength),
		"disable_web_page_preview": true, "reply_markup": map[string]any{"inline_keyboard": rows}}, nil)
}

// maxCallbackNote is Telegram's limit for a button's acknowledgement.
const maxCallbackNote = 200

// AnswerCallback acknowledges a pressed button, which stops it spinning, with a
// short note shown on top of the chat.
func (t *Telegram) AnswerCallback(ctx context.Context, id, text string) error {
	return t.call(ctx, t.client, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": truncate(text, maxCallbackNote)}, nil)
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
		Parameters  struct {
			RetryAfter int `json:"retry_after"` // seconds
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &reply); err != nil || !reply.OK {
		desc := reply.Description
		if desc == "" {
			desc = "an unreadable answer (" + strconv.Itoa(len(data)) + " bytes)"
		}
		return &APIError{Status: res.StatusCode, Description: desc, RetryAfter: time.Duration(reply.Parameters.RetryAfter) * time.Second}
	}
	if result != nil {
		return json.Unmarshal(reply.Result, result)
	}
	return nil
}
