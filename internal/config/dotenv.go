package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SetDotenv sets name to value in the .env file at path, or removes it when
// value is empty. Every other line stays as it is. The file is replaced
// atomically and is readable only by its owner. The value is single-quoted,
// so it is read back literally: no $ expansion, no # comment.
func SetDotenv(path, name, value string) error {
	if !envNameRe.MatchString(name) {
		return fmt.Errorf("%q is not a variable name", name)
	}
	if strings.ContainsAny(value, "'\r\n") {
		return errors.New("a value with a quote (') or a line break cannot be written to .env")
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var lines []string
	if text := strings.TrimRight(string(data), "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	line, written := name+"='"+value+"'", value == ""
	var out []string
	for _, l := range lines {
		if dotenvName(l) != name {
			out = append(out, l)
			continue
		}
		if !written { // the first definition is replaced in place, any other dropped
			out, written = append(out, line), true
		}
	}
	if !written {
		out = append(out, line)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".env.*.tmp") // created 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	text := ""
	if len(out) > 0 {
		text = strings.Join(out, "\n") + "\n"
	}
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// dotenvName is the variable a .env line sets, or "".
func dotenvName(line string) string {
	l := strings.TrimSpace(line)
	if strings.HasPrefix(l, "#") {
		return ""
	}
	l = strings.TrimSpace(strings.TrimPrefix(l, "export "))
	name, _, ok := strings.Cut(l, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(name)
}

// EnvOverrideName is the variable that overrides key from the environment:
// search.terms → AJA_CFG_SEARCH_TERMS.
func EnvOverrideName(key string) string {
	return EnvOverridePrefix + strings.ToUpper(strings.Replace(key, ".", "_", 1))
}
