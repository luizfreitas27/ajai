// Package remote is the daemon's remote commands: messages from the user's
// Telegram bot, read by long polling (nothing on the machine is reachable from
// the internet), acted on only when they come from the configured chat.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/store"
)

// Bot is the Telegram side: notify.Telegram.
type Bot interface {
	Updates(ctx context.Context, offset int64, wait time.Duration) ([]notify.Update, error)
	Send(ctx context.Context, text string) bool
	ChatID() string
	BotID() string
}

// Runs is the daemon's supervisor, as remote commands use it.
type Runs interface {
	Start(ctx context.Context, req daemon.Request) error
	Stop(reason string) (run.Holder, error)
}

const (
	// MaxAge: a /run or /stop that waited longer than this (the daemon was down,
	// or offline) is not acted on.
	MaxAge = 10 * time.Minute
	// offsetFresh: Telegram numbers updates afresh after a week without any, so
	// an older saved offset could skip the new ones; it is dropped instead.
	offsetFresh = 6 * 24 * time.Hour
	// ReasonStopped is the ending reason of a run stopped with /stop.
	ReasonStopped = "stopped from Telegram"
)

// Listener reads and acts on remote commands. Its methods run on Loop's
// goroutine only.
type Listener struct {
	bot     Bot
	runs    Runs
	store   *store.Store
	dataDir string
	logger  *slog.Logger
	now     func() time.Time

	poll        time.Duration                                    // the long-poll wait
	sleep       func(ctx context.Context, d time.Duration) error // between retries
	stopConfirm time.Duration                                    // how long /stop waits to say the run ended

	ignored    map[int64]bool // chats already logged as ignored
	confirming bool           // a /stop confirmation is already waiting
	mu         sync.Mutex     // guards confirming
	wg         sync.WaitGroup // /stop confirmations still waiting
}

// New makes a listener for the configured chat of bot.
func New(bot Bot, runs Runs, st *store.Store, dataDir string, logger *slog.Logger, now func() time.Time) *Listener {
	if logger == nil {
		logger = logging.Discard
	}
	return &Listener{bot: bot, runs: runs, store: st, dataDir: dataDir, logger: logger, now: now,
		poll: 50 * time.Second, sleep: sleep, stopConfirm: 5 * time.Minute, ignored: map[int64]bool{}}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Loop reads remote commands until ctx ends. Telegram or the network failing is
// one warning, retried with a growing pause, and one line when it works again.
// A refused token ends the loop; another reader of the same bot (a webhook, a
// second daemon) is reported once, on Telegram too.
func (l *Listener) Loop(ctx context.Context) {
	defer l.wg.Wait()
	if _, err := strconv.ParseInt(l.bot.ChatID(), 10, 64); err != nil {
		l.logger.Error("remote commands are off: AJA_TELEGRAM_CHAT_ID must be the numeric id of your chat with the bot")
		return
	}
	offset := l.loadOffset()
	backoff, failing, conflictSaid := 5*time.Second, false, false
	backlog := true // the first batch holds what arrived while nobody listened
	l.logger.Info("listening for remote commands")
	for ctx.Err() == nil {
		updates, err := l.bot.Updates(ctx, offset, l.poll)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var apiErr *notify.APIError
			switch {
			case errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized:
				l.logger.Error("remote commands stop: Telegram refused the bot token", "description", apiErr.Description)
				return
			case errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict:
				if !conflictSaid {
					conflictSaid = true
					l.logger.Error("remote commands: Telegram hands the bot's messages to someone else; retrying", "description", apiErr.Description)
					l.reply(ctx, "aja cannot read your commands: "+apiErr.Description+
						". Remove the bot's webhook (deleteWebhook), or stop the other program (another aja daemon?) reading this bot.")
				}
			case !failing:
				l.logger.Warn("remote commands: cannot reach Telegram; retrying", "error", err)
			}
			failing, backlog = true, true
			if l.sleep(ctx, backoff) != nil {
				return
			}
			backoff = min(backoff*2, 5*time.Minute)
			continue
		}
		if failing {
			l.logger.Info("remote commands: Telegram reachable again")
			failing, conflictSaid, backoff = false, false, 5*time.Second
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			// Saved before acting: a crash in between loses a command, never repeats one.
			if err := l.saveOffset(offset); err != nil {
				l.logger.Error("could not save the Telegram offset; not acting on the message", "error", err)
				continue
			}
			l.handle(ctx, u, backlog)
		}
		backlog = false
	}
}

// offsetKey is per bot: update ids are numbered per bot.
func (l *Listener) offsetKey() string { return "telegram.update_offset." + l.bot.BotID() }

type savedOffset struct {
	Offset int64     `json:"offset"`
	Saved  time.Time `json:"saved"`
}

func (l *Listener) loadOffset() int64 {
	v, err := l.store.GetState(l.offsetKey())
	if err != nil {
		l.logger.Warn("could not read the Telegram offset; old messages are checked by age", "error", err)
		return 0
	}
	var s savedOffset
	if json.Unmarshal([]byte(v), &s) != nil || time.Since(s.Saved) > offsetFresh {
		return 0
	}
	return s.Offset
}

func (l *Listener) saveOffset(offset int64) error {
	data, _ := json.Marshal(savedOffset{Offset: offset, Saved: time.Now()})
	return l.store.SetState(l.offsetKey(), string(data))
}

// handle acts on one update: a command from the configured chat. backlog marks
// messages that arrived while nobody listened.
func (l *Listener) handle(ctx context.Context, u notify.Update, backlog bool) {
	m := u.Message
	if m == nil || m.Text == "" {
		return
	}
	if strconv.FormatInt(m.Chat.ID, 10) != l.bot.ChatID() {
		if !l.ignored[m.Chat.ID] {
			l.ignored[m.Chat.ID] = true
			l.logger.Warn("ignoring Telegram messages from a chat that is not AJA_TELEGRAM_CHAT_ID", "chat", m.Chat.ID)
		}
		return
	}
	name, args := parse(m.Text)
	if name == "" {
		return // not a command
	}
	defer func() {
		if p := recover(); p != nil { // a bug in one command must not end the daemon
			l.logger.Error("remote command crashed", "command", name, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			l.reply(ctx, "aja hit a bug handling /"+name+"; it is in the daemon's log.")
		}
	}()
	l.logger.Info("remote command", "command", name)
	acts := name == "run" || name == "stop"
	if acts && backlog && l.now().Sub(m.Time()) > MaxAge {
		l.reply(ctx, "Not acting on /"+name+" sent at "+m.Time().Local().Format("15:04")+": aja was not listening then. Send it again.")
		return
	}
	switch name {
	case "status":
		l.reply(ctx, l.status())
	case "stop":
		l.stop(ctx, m.Time(), backlog)
	case "run":
		l.start(ctx, args)
	case "help", "start":
		l.reply(ctx, help())
	default:
		l.reply(ctx, "Unknown command /"+name+". /help lists them.")
	}
}

// parse reads "/run@aja_bot brasil dry" as "run", ["brasil", "dry"]; text that
// is not a command gives "".
func parse(text string) (string, []string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", nil
	}
	name, _, _ := strings.Cut(strings.ToLower(fields[0][1:]), "@")
	return name, fields[1:]
}

func (l *Listener) reply(ctx context.Context, text string) { l.bot.Send(ctx, text) }
