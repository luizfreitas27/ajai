// Package remote is the daemon's remote commands: messages from the user's
// Telegram bot, read by long polling (nothing on the machine is reachable from
// the internet), acted on only when they come from the configured chat.
package remote

import (
	"context"
	"log/slog"
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
}

// Runs is the daemon's supervisor, as remote commands use it.
type Runs interface {
	Start(ctx context.Context, req daemon.Request) error
	Stop(reason string) (run.Holder, error)
}

const (
	// offsetKey keeps the next update to read in the store's state, so a
	// restart never acts on a command twice.
	offsetKey = "telegram.update_offset"
	// MaxAge: a command older than this was sent while nobody listened (the
	// daemon was down); it is not acted on.
	MaxAge = 10 * time.Minute
	// ReasonStopped is the ending reason of a run stopped with /stop.
	ReasonStopped = "stopped from Telegram"
)

// Listener reads and acts on remote commands.
type Listener struct {
	bot     Bot
	runs    Runs
	store   *store.Store
	dataDir string
	logger  *slog.Logger
	now     func() time.Time

	// Poll is the long-poll wait; Sleep waits between retries (tests shorten both).
	Poll  time.Duration
	Sleep func(ctx context.Context, d time.Duration) error
	// StopConfirm bounds how long /stop waits to say the run has ended.
	StopConfirm time.Duration

	ignored map[int64]bool // chats already logged as ignored
	wg      sync.WaitGroup // /stop confirmations still waiting
}

// New makes a listener for the configured chat of bot.
func New(bot Bot, runs Runs, st *store.Store, dataDir string, logger *slog.Logger, now func() time.Time) *Listener {
	if logger == nil {
		logger = logging.Discard
	}
	return &Listener{bot: bot, runs: runs, store: st, dataDir: dataDir, logger: logger, now: now,
		Poll: 50 * time.Second, Sleep: sleep, StopConfirm: 5 * time.Minute, ignored: map[int64]bool{}}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Loop reads commands until ctx ends. Telegram or the network failing is one
// warning, retried with a growing pause, and one line when it works again.
func (l *Listener) Loop(ctx context.Context) {
	defer l.wg.Wait()
	offset := l.offset()
	backoff, failing := 5*time.Second, false
	l.logger.Info("listening for Telegram commands")
	for ctx.Err() == nil {
		updates, err := l.bot.Updates(ctx, offset, l.Poll)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !failing {
				l.logger.Warn("Telegram commands: cannot reach Telegram; retrying", "error", err)
				failing = true
			}
			if l.Sleep(ctx, backoff) != nil {
				return
			}
			backoff = min(backoff*2, 5*time.Minute)
			continue
		}
		if failing {
			l.logger.Info("Telegram commands: reachable again")
			failing, backoff = false, 5*time.Second
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			// Saved before acting: a crash in between loses a command, never repeats one.
			if err := l.store.SetState(offsetKey, strconv.FormatInt(offset, 10)); err != nil {
				l.logger.Warn("could not save the Telegram offset", "error", err)
			}
			l.Handle(ctx, u)
		}
	}
}

func (l *Listener) offset() int64 {
	v, _ := l.store.GetState(offsetKey)
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// Handle acts on one update: a command from the configured chat.
func (l *Listener) Handle(ctx context.Context, u notify.Update) {
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
	if age := l.now().Sub(m.Time()); age > MaxAge {
		l.reply(ctx, "Not acting on /"+name+" sent at "+m.Time().Local().Format("15:04")+": aja was not listening then. Send it again.")
		return
	}
	l.logger.Info("Telegram command", "command", name)
	switch name {
	case "status":
		l.reply(ctx, l.status())
	case "stop":
		l.stop(ctx)
	case "run":
		l.start(ctx, args)
	case "help", "start":
		l.reply(ctx, help)
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

const help = `aja listens to:
/status: the run in progress (or the last one), pending answers, the next window
/stop: stop the run in progress, wherever it was started
/run [brasil|internacional] [dry]: start an unattended run now
/help: this list`
