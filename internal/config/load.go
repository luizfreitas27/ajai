package config

import (
	"errors"
	"fmt"
	"io"
	"os"
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
}

// EnvOverridePrefix: AJA_CFG_SEARCH_TERMS sets search.terms. The section is the
// first token; the rest is the key with its underscores kept.
const EnvOverridePrefix = "AJA_CFG_"

// Environ lists every variable as KEY=VALUE, like os.Environ; used to find
// AJA_CFG_ overrides. Nil means "only the lookup", i.e. no prefix scan.
type Environ func() []string

// Load builds the configuration: defaults, then the YAML file (strict: unknown
// keys are errors), then AJA_CFG_ environment overrides, then flag overrides,
// then validation. The .env beside the config is read for secrets and overrides.
func Load(p paths.Paths, env Env, environ Environ, flags map[string]string) (*Loaded, error) {
	l := &Loaded{Config: Defaults(), Paths: p}

	data, err := os.ReadFile(p.ConfigFile)
	switch {
	case err == nil:
		l.FileExisted = true
		if err := decodeStrict(data, &l.Config); err != nil {
			return nil, fmt.Errorf("%s: %w", p.ConfigFile, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}

	if err := env.LoadDotenv(p.EnvFile); err != nil {
		return nil, fmt.Errorf("%s: %w", p.EnvFile, err)
	}
	l.Secrets = env.Secrets()

	for key, value := range envOverrides(env, environ) {
		if err := l.Config.Set(key, value); err != nil {
			return nil, fmt.Errorf("environment override %s%s: %w", EnvOverridePrefix, strings.ToUpper(strings.Replace(key, ".", "_", 1)), err)
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

func decodeStrict(data []byte, into *Config) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	err := dec.Decode(into)
	if err == nil || errors.Is(err, io.EOF) { // io.EOF: an empty file, nothing to overlay
		return nil
	}
	if m := unknownFieldRe.FindStringSubmatch(err.Error()); m != nil {
		if looksLikeSecret(m[1]) {
			return fmt.Errorf("key %q looks like a secret; secrets never go in the config file. Put it in .env or the environment as one of %s",
				m[1], strings.Join(SecretEnvNames, ", "))
		}
		return &UnknownKeyError{Key: m[1], Suggestions: suggest(m[1])}
	}
	return err
}

func envOverrides(env Env, environ Environ) map[string]string {
	out := map[string]string{}
	if environ != nil {
		for _, kv := range environ() {
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
		if sub, k2, ok := strings.Cut(key, "_"); ok && (sub == "brasil" || sub == "internacional") {
			return section + "." + sub + "." + k2
		}
	}
	return section + "." + key
}
