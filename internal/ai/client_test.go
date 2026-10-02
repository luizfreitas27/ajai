package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/questions"
)

// fakeModel is an OpenAI-compatible chat endpoint that answers with reply and
// records what it was sent.
type fakeModel struct {
	mu      sync.Mutex
	reply   string
	refusal string
	status  int
	bodies  []map[string]any
	auth    []string
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	f.bodies = append(f.bodies, body)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = fmt.Fprint(w, `{"error":{"message":"boom","type":"server_error"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": f.reply, "refusal": f.refusal})
	_, _ = fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":%s,"finish_reason":"stop"}]}`, msg)
}

func (f *fakeModel) prompt(t *testing.T) string {
	t.Helper()
	msgs := f.bodies[len(f.bodies)-1]["messages"].([]any)
	return msgs[0].(map[string]any)["content"].(string)
}

func client(t *testing.T, model *fakeModel, key string) (*Client, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)
	var log bytes.Buffer
	c, err := New(Settings{BaseURL: srv.URL + "/v1/", Model: "gpt-test", APIKey: key, Profile: "Name: Jane",
		Logger: slog.New(slog.NewTextHandler(&log, nil)), MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	return c, &log
}

func TestAnOptionIsAcceptedOnlyWhenNamedExactlyCaseAndQuotesTolerated(t *testing.T) {
	options := []string{"Yes", "No"}
	for raw, want := range map[string]string{
		"Yes": "Yes", "yes": "Yes", " YES ": "Yes", `"Yes"`: "Yes", "'No'": "No",
		"Yes, absolutely": "", "Maybe later": "", "Y": "", "": "",
	} {
		model := &fakeModel{reply: raw}
		c, log := client(t, model, "")
		got, err := c.Answer(context.Background(), "Do you have a driver's licence?", questions.Radio, options, "")
		if err != nil || got != want {
			t.Errorf("model said %q: got %q (%v), want %q", raw, got, err, want)
		}
		if want == "" && raw != "" && !strings.Contains(log.String(), "none of the options") {
			t.Errorf("a rejected paraphrase must be logged with the options:\n%s", log.String())
		}
	}
}

func TestATextAnswerIsTrimmedAndThePromptCarriesProfileQuestionOptionsAndJob(t *testing.T) {
	model := &fakeModel{reply: "  Go and Python.  \n"}
	c, _ := client(t, model, "")
	got, err := c.Answer(context.Background(), "Which languages?", questions.Text, nil, "We use Go.")
	if err != nil || got != "Go and Python." {
		t.Fatalf("%q %v", got, err)
	}
	p := model.prompt(t)
	for _, want := range []string{"Applicant information:\nName: Jane", "Question:\nWhich languages?", "Job description:\nWe use Go."} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if model.bodies[0]["model"] != "gpt-test" {
		t.Errorf("model %v", model.bodies[0]["model"])
	}
	if _, err := c.Answer(context.Background(), "Shift?", questions.Select, []string{"Day", "Night"}, ""); err != nil {
		t.Fatal(err)
	}
	if p := model.prompt(t); !strings.HasSuffix(p, "Answer with exactly one of these options:\n- Day\n- Night") {
		t.Errorf("%s", p)
	}
}

func TestARefusalAndAServerErrorGiveNoAnswer(t *testing.T) {
	model := &fakeModel{refusal: "I can't help with that."}
	c, log := client(t, model, "")
	if got, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); got != "" || err != nil || !strings.Contains(log.String(), "refused") {
		t.Errorf("%q %v %s", got, err, log.String())
	}
	model = &fakeModel{status: 500}
	c, _ = client(t, model, "")
	if got, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); got != "" || err == nil {
		t.Errorf("%q %v", got, err)
	}
	if len(model.bodies) != 1 {
		t.Errorf("retries were disabled, got %d calls", len(model.bodies))
	}
}

func TestTheKeyIsSentOnlyWhenSetAndNeverOverPlainHTTPToAnotherHost(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-the-environment")
	model := &fakeModel{reply: "ok"}
	c, _ := client(t, model, "")
	if _, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); err != nil {
		t.Fatal(err)
	}
	if model.auth[0] != "" {
		t.Errorf("a keyless provider sent %q (the environment's key must not leak)", model.auth[0])
	}
	model = &fakeModel{reply: "ok"}
	c, _ = client(t, model, "sk-mine")
	if _, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); err != nil {
		t.Fatal(err)
	}
	if model.auth[0] != "Bearer sk-mine" {
		t.Errorf("loopback with a key: %q", model.auth[0])
	}
	if _, err := New(Settings{BaseURL: "http://ollama.lan:11434/v1/", Model: "m", APIKey: "sk-mine"}); !errors.Is(err, ErrKeyOverHTTP) {
		t.Errorf("%v", err)
	}
	// Without a key, a LAN server over plain HTTP is fine.
	lan := &fakeModel{reply: "ok"}
	srv := httptest.NewServer(lan)
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}}
	c, err := New(Settings{BaseURL: "http://ollama.lan:11434/v1/", Model: "m", HTTPClient: hc, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); err != nil || got != "ok" || lan.auth[0] != "" {
		t.Errorf("%q %v %q", got, err, lan.auth)
	}
}

func TestFromConfigNeedsAKeyForHostedProvidersOnly(t *testing.T) {
	c := config.Defaults()
	m := markets.Resolve(c)
	if cl, err := FromConfig(c, config.Secrets{}, m, nil); cl != nil || err != nil {
		t.Errorf("AI off: %v %v", cl, err)
	}
	c.AI.Enabled = true
	if _, err := FromConfig(c, config.Secrets{}, m, nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("openai without a key: %v", err)
	}
	c.AI.Provider = "ollama"
	if cl, err := FromConfig(c, config.Secrets{}, m, nil); cl == nil || err != nil {
		t.Errorf("ollama without a key: %v", err)
	}
	c.AI.Provider = "openai"
	if cl, err := FromConfig(c, config.Secrets{AIAPIKey: "sk-x"}, m, nil); cl == nil || err != nil {
		t.Errorf("%v", err)
	}
}

func TestSensitiveQuestionsNeverReachTheModelEndToEnd(t *testing.T) {
	model := &fakeModel{reply: "Yes"}
	c, _ := client(t, model, "")
	cfg := config.Defaults()
	cfg.Questions.RequireVisa, cfg.Questions.LegallyAuthorized, cfg.Questions.Citizenship = "", "", ""
	cfg.Questions.DesiredSalary = 0
	cfg.Personals.Veteran, cfg.Personals.Disability = "", ""
	a := questions.New(cfg, nil, c, nil)
	for _, q := range []questions.Question{
		{Label: "Will you require visa sponsorship?", Kind: questions.Select, Options: []string{"Select an option", "Yes", "No"}, Current: "Select an option"},
		{Label: "Are you a protected veteran?", Kind: questions.Radio, Options: []string{"Yes", "No"}},
		{Label: "Do you have a disability?", Kind: questions.Radio, Options: []string{"Yes", "No"}},
		{Label: "Do you hold an active security clearance?", Kind: questions.Text},
		{Label: "Pretensão salarial", Kind: questions.Text},
		{Label: "Email address", Kind: questions.Text},
	} {
		a.Answer(q, questions.Job{})
	}
	if len(model.bodies) != 0 {
		t.Errorf("the model was asked %d sensitive question(s)", len(model.bodies))
	}
	a.Answer(questions.Question{Label: "Preferred shift", Kind: questions.Radio, Options: []string{"Day", "Night"}}, questions.Job{})
	if len(model.bodies) != 1 {
		t.Error("an ordinary question should reach the model")
	}
}
