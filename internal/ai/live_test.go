package ai

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/questions"
)

// TestLiveOpenAIAnswer makes one real call; it runs only when AJA_LIVE_OPENAI_KEY
// is set (never in CI). AJA_LIVE_MODEL picks the model (default: the config default).
func TestLiveOpenAIAnswer(t *testing.T) {
	key := os.Getenv("AJA_LIVE_OPENAI_KEY")
	if key == "" {
		t.Skip("set AJA_LIVE_OPENAI_KEY to run the live smoke test")
	}
	model := os.Getenv("AJA_LIVE_MODEL")
	if model == "" {
		model = config.Defaults().AI.Model
	}
	c, err := New(Settings{BaseURL: config.ProviderBaseURLs["openai"], Model: model, APIKey: key, Profile: "Name: Jane\nYears of professional experience: 6"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	got, err := c.Answer(ctx, "Are you comfortable working remotely?", questions.Radio, []string{"Yes", "No"}, "")
	if err != nil || (got != "Yes" && got != "No") {
		t.Errorf("%q %v", got, err)
	}
}
