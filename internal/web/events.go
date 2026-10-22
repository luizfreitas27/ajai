package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/logging"
)

// backlogLines is how many of the day's log lines a client gets on connecting.
const backlogLines = 300

// backlog returns the day's last backlogLines complete lines and the offset to
// start the tail at: just past the last newline, so a line still being written
// reaches the tail whole.
// ponytail: reads the whole file once per connection (~100 KB a day); seek back from the end if logs grow large.
func backlog(path string) ([]string, int64, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	var lines []string
	for _, line := range strings.Split(string(data[:end]), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > backlogLines {
		lines = lines[len(lines)-backlogLines:]
	}
	return lines, int64(end), nil
}

// events streams the day's log lines as they are written and the status every
// interval, until the client goes away or the server shuts down. One reader
// follows the log per connection.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	now := s.d.Now()
	path := logging.FileName(s.d.LogDir, now)
	past, offset, err := backlog(path) // the lines written from offset on are followed
	if err != nil {
		s.log.Warn("the web front could not read the log", "error", err)
		past = nil
		if fi, err := os.Stat(path); err == nil {
			offset = fi.Size()
		}
	}
	tail := logging.NewTail(logging.FileName, s.d.LogDir, now, offset)
	logs := time.NewTicker(min(s.every, time.Second))
	defer logs.Stop()
	status := time.NewTicker(s.every)
	defer status.Stop()

	send := func(event string, data []byte) bool {
		if _, err := w.Write([]byte("event: " + event + "\ndata: " + string(data) + "\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	sendStatus := func() bool {
		data, err := json.Marshal(s.statusNow(r.Context()))
		return err == nil && send("status", data)
	}
	if !sendStatus() {
		return
	}
	for _, line := range past {
		if !send("log", []byte(line)) {
			return
		}
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-logs.C:
			text, err := tail.Read(s.d.Now())
			if err != nil {
				s.log.Warn("the web front could not read the log", "error", err)
				continue
			}
			for _, line := range strings.Split(string(text), "\n") {
				if line != "" && !send("log", []byte(line)) {
					return
				}
			}
		case <-status.C:
			if !sendStatus() {
				return
			}
		}
	}
}
