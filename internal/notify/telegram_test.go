package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	token = "123456789:AAHtoken"
	chat  = "987654321"
)

type sent struct {
	path, method, contentType string
	body                      map[string]any
}

// server answers like the Bot API; status 0 means 200.
func server(t *testing.T, status int, reply string) *[]sent {
	t.Helper()
	var got []sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		got = append(got, sent{r.URL.Path, r.Method, r.Header.Get("Content-Type"), body})
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	saved := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = saved })
	return &got
}

func logger() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

func TestAConfiguredSendPostsJSONToTheBotAPI(t *testing.T) {
	got := server(t, 0, `{"ok":true}`)
	tg := New(token, chat, nil)
	if !tg.Send(context.Background(), "Run finished.\nApplied: 3") {
		t.Fatal("send failed")
	}
	s := (*got)[0]
	if s.path != "/bot"+token+"/sendMessage" || s.method != "POST" || s.contentType != "application/json" {
		t.Errorf("%+v", s)
	}
	if s.body["chat_id"] != chat || s.body["text"] != "Run finished.\nApplied: 3" || s.body["disable_web_page_preview"] != true {
		t.Errorf("%+v", s.body)
	}
	if tg.client.Timeout.Seconds() != 10 {
		t.Errorf("timeout %v", tg.client.Timeout)
	}
}

func TestNothingIsSentOrLoggedWhenBothSettingsAreEmpty(t *testing.T) {
	got := server(t, 0, `{"ok":true}`)
	for _, pair := range [][2]string{{"", ""}, {"  ", ""}} {
		l, log := logger()
		if New(pair[0], pair[1], l).Send(context.Background(), "x") || len(*got) != 0 || log.Len() != 0 {
			t.Errorf("%q: sent %d, log %q", pair, len(*got), log.String())
		}
	}
	var zero *Telegram
	if zero.Send(context.Background(), "x") || zero.Configured() {
		t.Error("a nil notifier is off")
	}
}

func TestHalfConfiguredSendsNothingAndWarnsOnce(t *testing.T) {
	got := server(t, 0, `{"ok":true}`)
	for _, pair := range [][2]string{{token, ""}, {"", chat}, {"   ", chat}} {
		l, log := logger()
		tg := New(pair[0], pair[1], l)
		tg.Send(context.Background(), "x")
		tg.Send(context.Background(), "y")
		if len(*got) != 0 || strings.Count(log.String(), "BOTH") != 1 || strings.Contains(log.String(), "ERROR") {
			t.Errorf("%q: %s", pair, log.String())
		}
	}
}

func TestFailuresAreWarningsAndNeverLogTheToken(t *testing.T) {
	cases := []struct {
		name   string
		status int
		reply  string
		base   string // overrides the test server (a network failure)
		want   []string
	}{
		{"rejected", 401, `{"ok":false,"description":"Unauthorized"}`, "", []string{"401", "Unauthorized"}},
		{"server error", 500, `oops`, "", []string{"500"}},
		// The client error would quote the URL, token included.
		{"network failure", 0, "", "http://127.0.0.1:1", []string{"could not send"}},
	}
	for _, tc := range cases {
		server(t, tc.status, tc.reply)
		l, log := logger()
		tg := New(token, chat, l)
		if tc.base != "" {
			tg.WithAPIBase(tc.base)
		}
		if tg.Send(context.Background(), "x") {
			t.Errorf("%s: accepted", tc.name)
		}
		for _, want := range tc.want {
			if !strings.Contains(log.String(), want) {
				t.Errorf("%s: log lacks %q: %s", tc.name, want, log.String())
			}
		}
		if strings.Contains(log.String(), token) || !strings.Contains(log.String(), "WARN") {
			t.Errorf("%s: %s", tc.name, log.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l, log := logger()
	if New(token, chat, l).WithAPIBase("http://127.0.0.1:1").Send(ctx, "x") || strings.Contains(log.String(), "WARN") {
		t.Errorf("a send abandoned because the run ended is not a warning: %s", log.String())
	}
}

func TestLongMessagesAreCutToTelegramsLimit(t *testing.T) {
	got := server(t, 0, `{"ok":true}`)
	New(token, chat, nil).Send(context.Background(), strings.Repeat("é", 5000))
	if n := utf8.RuneCountInString((*got)[0].body["text"].(string)); n != MaxMessageLength {
		t.Errorf("%d characters", n)
	}
}
