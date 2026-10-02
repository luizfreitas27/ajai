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
		data, retired, err := dropRetired(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.ConfigFile, err)
		}
		for _, key := range retired {
			l.Warnings = append(l.Warnings, fmt.Sprintf("%s is no longer used and was ignored: %s (remove it, or run any `aja config set` to rewrite the file)", key, RetiredKeys[key]))
		}
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

	for key, value := range envOverrides(env) {
		if why, ok := RetiredKeys[key]; ok {
			l.Warnings = append(l.Warnings, fmt.Sprintf("the environment override for %s was ignored: %s", key, why))
			continue
		}
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

// dropRetired removes RetiredKeys from a YAML document and reports which it found.
// The file is only rewritten in memory; comments elsewhere are irrelevant here.
func dropRetired(data []byte) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil, nil
	}
	var found []string
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		section, body := root.Content[i].Value, root.Content[i+1]
		if body.Kind != yaml.MappingNode {
			continue
		}
		kept := body.Content[:0]
		for j := 0; j+1 < len(body.Content); j += 2 {
			key := section + "." + body.Content[j].Value
			if _, retired := RetiredKeys[key]; retired {
				found = append(found, key)
				continue
			}
			kept = append(kept, body.Content[j], body.Content[j+1])
		}
		body.Content = kept
	}
	if len(found) == 0 {
		return data, nil, nil
	}
	out, err := yaml.Marshal(&doc)
	return out, found, err
}

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
