package config

import (
	"errors"
	"fmt"
	"os"
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
	if strings.ContainsAny(value, "'\\\r\n") {
		return errors.New("a value with a quote ('), a backslash or a line break cannot be written to .env")
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
	text := ""
	if len(out) > 0 {
		text = strings.Join(out, "\n") + "\n"
	}
	return writeFileAtomic(path, []byte(text))
}

// dotenvName is the variable a .env line sets, or "", reading the line as
// godotenv does: an optional "export" and blanks, then the name up to "=" or
// ":".
func dotenvName(line string) string {
	l := strings.TrimSpace(line)
	if strings.HasPrefix(l, "#") {
		return ""
	}
	if rest, ok := strings.CutPrefix(l, "export"); ok && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
		l = strings.TrimSpace(rest)
	}
	end := strings.IndexAny(l, "=:")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(l[:end])
}
