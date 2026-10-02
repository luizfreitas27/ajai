package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Secrets are read from the environment only. They are never written to YAML.
type Secrets struct {
	AIAPIKey         string
	TelegramBotToken string
	TelegramChatID   string
}

// Secret environment variable names.
const (
	EnvAIAPIKey         = "AJA_AI_API_KEY"
	EnvTelegramBotToken = "AJA_TELEGRAM_BOT_TOKEN"
	EnvTelegramChatID   = "AJA_TELEGRAM_CHAT_ID"
)

// SecretEnvNames lists every secret variable, for doctor and import to print.
var SecretEnvNames = []string{EnvAIAPIKey, EnvTelegramBotToken, EnvTelegramChatID}

// secretYAMLKeys are names that look like secrets; finding one in the YAML is an
// error with a pointer to .env instead of a generic "unknown key".
var secretYAMLKeys = []string{"api_key", "apikey", "token", "password", "secret", "chat_id"}

func looksLikeSecret(key string) bool {
	k := strings.ToLower(key)
	for _, s := range secretYAMLKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// Env is an environment: a lookup, an optional listing (for prefix scans), and
// the ability to read a .env file. The real variable always beats the file.
type Env struct {
	Lookup  func(string) (string, bool)
	Environ func() []string // KEY=VALUE lines, like os.Environ; nil means "no scan"
	file    map[string]string
}

// OSEnv is the process environment.
func OSEnv() Env { return Env{Lookup: os.LookupEnv, Environ: os.Environ} }

// MapEnv is a fixed environment, for tests.
func MapEnv(m map[string]string) Env {
	return Env{
		Lookup: func(k string) (string, bool) { v, ok := m[k]; return v, ok },
		Environ: func() []string {
			out := make([]string, 0, len(m))
			for k, v := range m {
				out = append(out, k+"="+v)
			}
			return out
		},
	}
}

// Getenv is Get with the signature paths.Resolve and friends expect.
func (e Env) Getenv(key string) string { return e.Get(key) }

// LoadDotenv reads a .env file into the environment's lower layer. A missing
// file is not an error.
func (e *Env) LoadDotenv(path string) error {
	m, err := godotenv.Read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	e.file = m
	return nil
}

// Get returns the variable from the real environment, else from the .env file.
func (e Env) Get(key string) string {
	if v, ok := e.Lookup(key); ok {
		return v
	}
	return e.file[key]
}

// Secrets reads the three secrets.
func (e Env) Secrets() Secrets {
	return Secrets{
		AIAPIKey:         strings.TrimSpace(e.Get(EnvAIAPIKey)),
		TelegramBotToken: strings.TrimSpace(e.Get(EnvTelegramBotToken)),
		TelegramChatID:   strings.TrimSpace(e.Get(EnvTelegramChatID)),
	}
}

// Values lists every secret as (variable name, value) in SecretEnvNames order,
// so output code never spells the names twice.
func (s Secrets) Values() [][2]string {
	return [][2]string{
		{EnvAIAPIKey, s.AIAPIKey},
		{EnvTelegramBotToken, s.TelegramBotToken},
		{EnvTelegramChatID, s.TelegramChatID},
	}
}

// Mask is what secrets look like in any output.
const Mask = "********"
