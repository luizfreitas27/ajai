package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/questions"
)

// Settings configure a Client.
type Settings struct {
	BaseURL    string // OpenAI-compatible endpoint, e.g. https://api.openai.com/v1/
	Model      string
	APIKey     string // "" for local servers; then no Authorization header is ever sent
	Profile    string // the candidate profile
	Logger     *slog.Logger
	MaxRetries int           // default 1
	Timeout    time.Duration // per request, default 60 s
	HTTPClient *http.Client  // tests only
}

// Client answers form questions with a chat model.
type Client struct {
	chat    openai.ChatCompletionService
	model   string
	profile string
	logger  *slog.Logger
}

// ErrKeyOverHTTP: an API key is sent only over HTTPS or to this machine.
var ErrKeyOverHTTP = errors.New("an API key is only sent over HTTPS or to this machine (localhost); use an https:// base_url, or a local server without a key")

// New builds a client. With a key the endpoint must be HTTPS or loopback (the
// SDK's own rule, checked here first for a clear message); without one, any
// endpoint works and no Authorization header is sent. The SDK's environment
// defaults (OPENAI_API_KEY, OPENAI_ORG_ID, OPENAI_PROJECT_ID,
// OPENAI_CUSTOM_HEADERS, OPENAI_BASE_URL) are never read: whatever the user's
// shell holds for other tools does not reach a provider aja talks to.
func New(s Settings) (*Client, error) {
	if s.Logger == nil {
		s.Logger = slog.New(slog.DiscardHandler)
	}
	if strings.TrimSpace(s.Model) == "" {
		return nil, errors.New("ai.model is empty")
	}
	u, err := url.Parse(s.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("ai.base_url %q is not an http(s) URL", s.BaseURL)
	}
	if s.MaxRetries == 0 {
		s.MaxRetries = 1
	}
	if s.MaxRetries < 0 {
		s.MaxRetries = 0
	}
	if s.Timeout == 0 {
		s.Timeout = 60 * time.Second
	}
	httpClient := s.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	// The chat service built directly, not through openai.NewClient, which would
	// prepend the environment defaults.
	opts := []option.RequestOption{
		option.WithHTTPClient(httpClient),
		option.WithEnvironmentProduction(),
		option.WithBaseURL(s.BaseURL),
		option.WithMaxRetries(s.MaxRetries),
		option.WithRequestTimeout(s.Timeout),
	}
	if s.APIKey != "" {
		if u.Scheme == "http" && !loopback(u.Hostname()) {
			return nil, ErrKeyOverHTTP
		}
		opts = append(opts, option.WithAPIKey(s.APIKey), option.WithUnsafeAllowHTTP())
	}
	return &Client{chat: openai.NewChatCompletionService(opts...), model: s.Model, profile: s.Profile, logger: s.Logger}, nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ErrNoKey: the provider needs a key and none is set.
var ErrNoKey = errors.New("ai.enabled is true but " + config.EnvAIAPIKey + " is not set (environment or .env)")

// FromConfig builds the client for the configured provider and market. It
// returns (nil, nil) when ai.enabled is false.
func FromConfig(c config.Config, secrets config.Secrets, market markets.Resolved, logger *slog.Logger) (*Client, error) {
	if !c.AI.Enabled {
		return nil, nil
	}
	if secrets.AIAPIKey == "" && !config.ProvidersWithoutKey[c.AI.Provider] && c.AI.Provider != "custom" {
		return nil, ErrNoKey
	}
	return New(Settings{BaseURL: c.AIBaseURL(), Model: c.AI.Model, APIKey: secrets.AIAPIKey,
		Profile: CandidateProfile(c, market), Logger: logger})
}

// Answer asks the model one question. With options, the answer is the option
// it named exactly (SelectOption) or "". Satisfies questions.AI.
func (c *Client) Answer(ctx context.Context, question string, kind questions.Kind, options []string, jobDescription string) (string, error) {
	if strings.TrimSpace(question) == "" {
		return "", nil
	}
	res, err := c.chat.New(ctx, openai.ChatCompletionNewParams{
		Model:    c.model,
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(BuildPrompt(c.profile, question, jobDescription, options))},
	})
	if err != nil {
		return "", err
	}
	if len(res.Choices) == 0 {
		return "", errors.New("the model returned no choices")
	}
	msg := res.Choices[0].Message
	if msg.Content == "" && msg.Refusal != "" {
		c.logger.Warn("the model refused to answer", "question", question, "refusal", msg.Refusal)
		return "", nil
	}
	raw := strings.TrimSpace(msg.Content)
	if raw == "" {
		c.logger.Warn("the model returned an empty answer", "question", question)
		return "", nil
	}
	if len(options) == 0 {
		return raw, nil
	}
	if picked := SelectOption(raw, options); picked != "" {
		return picked, nil
	}
	c.logger.Warn("the model's answer is none of the options; rejected", "question", question, "answer", raw, "options", strings.Join(options, " | "))
	return "", nil
}
