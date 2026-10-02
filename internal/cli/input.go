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

type lineResult struct {
	text string
	err  error
}

// readLine returns the next line typed on stdin, without its newline. It gives
// up when ctx ends or, when timeout > 0, after timeout. One goroutine owns stdin
// for the whole process, so a wait abandoned by Ctrl-C never races a later one.
func (a *App) readLine(ctx context.Context, timeout time.Duration) (string, error) {
	a.linesOnce.Do(func() {
		ch := make(chan lineResult)
		a.lines = ch
		go func() {
			r := bufio.NewReader(a.Stdin)
			for {
				text, err := r.ReadString('\n')
				if text != "" {
					ch <- lineResult{text: strings.TrimRight(text, "\r\n")}
				}
				if err != nil {
					close(ch)
					return
				}
			}
		}()
	})
	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}
	select {
	case res, ok := <-a.lines:
		if !ok {
			return "", io.EOF
		}
		return res.text, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-deadline:
		return "", ErrNoAnswer
	}
}
