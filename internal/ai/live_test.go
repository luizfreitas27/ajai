package ai

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/questions"
)

// TestLiveOpenAIAnswer makes one real call; it runs only when AJAI_LIVE_OPENAI_KEY
// is set (never in CI). AJAI_LIVE_MODEL picks the model (default: the config default).
func TestLiveOpenAIAnswer(t *testing.T) {
	key := os.Getenv("AJAI_LIVE_OPENAI_KEY")
	if key == "" {
		t.Skip("set AJAI_LIVE_OPENAI_KEY to run the live smoke test")
	}
	model := os.Getenv("AJAI_LIVE_MODEL")
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
