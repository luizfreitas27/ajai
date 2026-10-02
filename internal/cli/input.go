package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// ErrNoAnswer: nobody typed anything within the allowed time.
var ErrNoAnswer = errors.New("nobody answered in time")

// readLine returns the next line typed on stdin, without its newline. It gives
// up when ctx ends or, when timeout > 0, after timeout. One goroutine owns stdin
// for the whole process, so a wait abandoned by Ctrl-C never races a later one.
func (a *App) readLine(ctx context.Context, timeout time.Duration) (string, error) {
	a.startLines()
	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}
	select {
	case text, ok := <-a.lines:
		if !ok {
			a.stdinClosed = true
			return "", io.EOF
		}
		return text, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-deadline:
		return "", ErrNoAnswer
	}
}

// startLines starts the one goroutine that reads stdin.
func (a *App) startLines() {
	a.linesOnce.Do(func() {
		ch := make(chan string)
		a.lines = ch
		go func() {
			r := bufio.NewReader(a.Stdin)
			for {
				text, err := r.ReadString('\n')
				if text != "" {
					ch <- strings.TrimRight(text, "\r\n")
				}
				if err != nil {
					close(ch)
					return
				}
			}
		}()
	})
}

// drainLines throws away whatever was typed before now (an Enter pressed
// during the login wait, or after a pause already gave up), so that it cannot
// answer the next question. It waits briefly for lines the terminal had buffered.
func (a *App) drainLines() {
	a.startLines()
	for {
		select {
		case _, ok := <-a.lines:
			if !ok {
				a.stdinClosed = true
				return
			}
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}
