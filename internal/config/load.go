package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/luizfreitas27/aja/internal/paths"
)

// Loaded is the result of building the configuration once for this process.
type Loaded struct {
	Config  Config
	Secrets Secrets
	Paths   paths.Paths
	// FileExisted is false when no config file was found and defaults were used.
	FileExisted bool
	// Overrides lists the keys set from the environment or flags, for `config show`.
	Overrides []string
	// Warnings are things the user should fix but that do not stop a run, such as a
	// retired key still present in the config file.
	Warnings []string
}

// RetiredKeys are settings aja no longer has, with what replaces them. A config
// file (or an AJA_CFG_ override) that still sets one loads with a warning: an old
// file must keep working. "aja config set" rewrites the file without them.
var RetiredKeys = map[string]string{
	"settings.click_gap": "the pause between form actions is pacing.between_actions_min_seconds / pacing.between_actions_max_seconds",
}

// EnvOverridePrefix: AJA_CFG_SEARCH_TERMS sets search.terms. The section is the
// first token; the rest is the key with its underscores kept.
const EnvOverridePrefix = "AJA_CFG_"

// Load builds the configuration: defaults, then the YAML file (strict: unknown
// keys are errors), then AJA_CFG_ environment overrides, then flag overrides,
// then validation. The .env beside the config is read for secrets and overrides.
func Load(p paths.Paths, env Env, flags map[string]string) (*Loaded, error) {
	l := &Loaded{Config: Defaults(), Paths: p}

	data, err := os.ReadFile(p.ConfigFile)
	switch {
	case err == nil:
		l.FileExisted = true
		retired, err := decodeStrict(data, &l.Config)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.ConfigFile, err)
		}
		for _, key := range retired {
			l.Warnings = append(l.Warnings, fmt.Sprintf("%s is no longer used and was ignored: %s (remove it, or run any `aja config set` to rewrite the file)", key, RetiredKeys[key]))
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}

	if err := env.LoadDotenv(p.EnvFile); err != nil {
		return nil, fmt.Errorf("%s: %w", p.EnvFile, err)
	}
	l.Secrets = env.Secrets()

	for key, value := range envOverrides(env) {
		if why, ok := RetiredKeys[key]; ok {
			l.Warnings = append(l.Warnings, fmt.Sprintf("the environment override for %s was ignored: %s", key, why))
			continue
		}
		if err := l.Config.Set(key, value); err != nil {
			return nil, fmt.Errorf("environment override %s: %w", EnvOverrideName(key), err)
		}
		l.Overrides = append(l.Overrides, key)
	}
	for key, value := range flags {
		if err := l.Config.Set(key, value); err != nil {
			return nil, fmt.Errorf("flag override %s: %w", key, err)
		}
		l.Overrides = append(l.Overrides, key)
	}
	sort.Strings(l.Overrides)

	if err := l.Config.Validate(); err != nil {
		return l, err
	}
	return l, nil
}

var unknownFieldRe = regexp.MustCompile(`field (\S+) not found`)

// decodeStrict overlays a YAML document on into. Unknown keys are errors, except
// RetiredKeys, which it returns instead. The decoder carries on past an unknown
// key, so a retired one is simply left out, and every error keeps its line number.
func decodeStrict(data []byte, into *Config) ([]string, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	err := dec.Decode(into)
	if err == nil || errors.Is(err, io.EOF) { // io.EOF: an empty file, nothing to overlay
		return nil, nil
	}
	var retired []string
	var te *yaml.TypeError
	if errors.As(err, &te) {
		var rest []string
		for _, msg := range te.Errors {
			if key := retiredKeyIn(msg); key != "" {
				retired = append(retired, key)
				continue
			}
			rest = append(rest, msg)
		}
		if len(rest) == 0 {
			return retired, nil
		}
		err = &yaml.TypeError{Errors: rest}
	}
	if m := unknownFieldRe.FindStringSubmatch(err.Error()); m != nil {
		if looksLikeSecret(m[1]) {
			return nil, fmt.Errorf("key %q looks like a secret; secrets never go in the config file. Put it in .env or the environment as one of %s",
				m[1], strings.Join(SecretEnvNames, ", "))
		}
		return nil, &UnknownKeyError{Key: m[1], Suggestions: suggest(m[1])}
	}
	return nil, err
}

// fieldNotFoundRe is one of the decoder's unknown-key errors.
var fieldNotFoundRe = regexp.MustCompile(`^line \d+: field (\S+) not found in type (\S+)$`)

// retiredKeyIn returns the RetiredKeys entry an unknown-key error is about, or "".
func retiredKeyIn(msg string) string {
	m := fieldNotFoundRe.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	for key := range RetiredKeys {
		section, name, _ := strings.Cut(key, ".")
		if name == m[1] && sectionType(section) == m[2] {
			return key
		}
	}
	return ""
}

// sectionType is the Go type of a top-level section ("settings" → "config.Settings"),
// as the decoder names it in its errors.
func sectionType(section string) string {
	typ := reflect.TypeOf(Config{})
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); yamlName(f) == section {
			return f.Type.String()
		}
	}
	return ""
}

func envOverrides(env Env) map[string]string {
	out := map[string]string{}
	if env.Environ != nil {
		for _, kv := range env.Environ() {
			if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, EnvOverridePrefix) {
				if key := envKeyToDotted(k); key != "" {
					out[key] = v
				}
			}
		}
	}
	for k, v := range env.file {
		if strings.HasPrefix(k, EnvOverridePrefix) {
			if key := envKeyToDotted(k); key != "" {
				if _, set := out[key]; !set {
					out[key] = v
				}
			}
		}
	}
	return out
}

func envKeyToDotted(k string) string {
	rest := strings.ToLower(strings.TrimPrefix(k, EnvOverridePrefix))
	section, key, ok := strings.Cut(rest, "_")
	if !ok {
		return ""
	}
	// markets has one more level: AJA_CFG_MARKETS_BRASIL_TERMS -> markets.brasil.terms
	if section == "markets" {
		if sub, k2, ok := strings.Cut(key, "_"); ok && IsValidMarket(sub) {
			return section + "." + sub + "." + k2
		}
	}
	return section + "." + key
}
