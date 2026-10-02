package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Field describes one configurable key, from the struct tags.
type Field struct {
	Key     string // dotted, e.g. "search.terms"
	Doc     string
	Options []string // closed set for strings / string lists; nil when open
	Example string
	Min     *int
	Kind    reflect.Kind // String, Int, Bool, Slice (of string)
}

// fieldRef is a Field plus where it lives in a Config value.
type fieldRef struct {
	Field
	value reflect.Value
}

func walk(v reflect.Value, prefix string, visit func(fieldRef)) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		name := yamlName(sf)
		if name == "" || name == "-" {
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		fv := v.Field(i)
		if sf.Type.Kind() == reflect.Struct {
			walk(fv, key, visit)
			continue
		}
		f := Field{Key: key, Doc: sf.Tag.Get("doc"), Example: sf.Tag.Get("example"), Kind: sf.Type.Kind()}
		f.Options = options(sf)
		if m, ok := sf.Tag.Lookup("min"); ok {
			n, _ := strconv.Atoi(m)
			f.Min = &n
		}
		visit(fieldRef{Field: f, value: fv})
	}
}

// Fields lists every key in file order.
func Fields() []Field {
	var out []Field
	c := Config{}
	walk(reflect.ValueOf(&c).Elem(), "", func(r fieldRef) { out = append(out, r.Field) })
	return out
}

// Sections lists the top-level section names in file order.
func Sections() []string {
	t := reflect.TypeOf(Config{})
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out = append(out, yamlName(t.Field(i)))
	}
	return out
}

// yamlName is the key a struct field is written under.
func yamlName(sf reflect.StructField) string { return strings.Split(sf.Tag.Get("yaml"), ",")[0] }

// options is the closed value set from the `opts` tag, nil when the field is open.
func options(sf reflect.StructField) []string {
	if opts, ok := sf.Tag.Lookup("opts"); ok {
		return strings.Split(opts, "|")
	}
	return nil
}

func (c *Config) find(key string) (fieldRef, bool) {
	var found fieldRef
	ok := false
	walk(reflect.ValueOf(c).Elem(), "", func(r fieldRef) {
		if r.Key == key {
			found, ok = r, true
		}
	})
	return found, ok
}

// UnknownKeyError names a key that does not exist, with suggestions.
type UnknownKeyError struct {
	Key         string
	Suggestions []string
}

// Error formats the key and the suggestions.
func (e *UnknownKeyError) Error() string {
	msg := fmt.Sprintf("unknown key %q", e.Key)
	if len(e.Suggestions) > 0 {
		msg += "; did you mean " + strings.Join(e.Suggestions, ", ") + "?"
	}
	return msg
}

func suggest(key string) []string {
	last := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		last = key[i+1:]
	}
	var out []string
	for _, f := range Fields() {
		fieldLast := f.Key[strings.LastIndex(f.Key, ".")+1:]
		if strings.Contains(f.Key, last) || editDistance(fieldLast, last) <= 2 {
			out = append(out, f.Key)
		}
	}
	sort.Strings(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// Get returns the value at a dotted key as text: lists are comma-joined.
func (c *Config) Get(key string) (string, error) {
	r, ok := c.find(key)
	if !ok {
		return "", &UnknownKeyError{Key: key, Suggestions: suggest(key)}
	}
	return formatValue(r.value), nil
}

func formatValue(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Slice:
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = v.Index(i).String()
		}
		return strings.Join(parts, ", ")
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int:
		return strconv.FormatInt(v.Int(), 10)
	default:
		return v.String()
	}
}

// Set parses text into the key's type and assigns it. Lists are comma-separated
// (an empty string clears the list). It does not validate options; call
// Validate afterwards so the message can quote the valid values.
func (c *Config) Set(key, text string) error {
	r, ok := c.find(key)
	if !ok {
		return &UnknownKeyError{Key: key, Suggestions: suggest(key)}
	}
	switch r.value.Kind() {
	case reflect.String:
		r.value.SetString(text)
	case reflect.Bool:
		b, err := strconv.ParseBool(strings.ToLower(strings.TrimSpace(text)))
		if err != nil {
			return &FieldError{Key: key, Problem: fmt.Sprintf("%q is not a boolean", text), Valid: []string{"true", "false"}}
		}
		r.value.SetBool(b)
	case reflect.Int:
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			return &FieldError{Key: key, Problem: fmt.Sprintf("%q is not a whole number", text)}
		}
		r.value.SetInt(int64(n))
	case reflect.Slice:
		r.value.Set(reflect.ValueOf(splitList(text)))
	default:
		return fmt.Errorf("%s: unsupported type %s", key, r.value.Kind())
	}
	return nil
}

// SetChecked sets a key and validates the whole config. It returns a *FieldError
// when the new value itself is invalid (the caller should refuse the write), and
// otherwise the remaining problems elsewhere in the config, if any, as a
// *ValidationError the caller may warn about.
func (c *Config) SetChecked(key, text string) (other *ValidationError, err error) {
	if err := c.Set(key, text); err != nil {
		return nil, err
	}
	verr := c.Validate()
	if verr == nil {
		return nil, nil
	}
	ve, _ := AsValidation(verr)
	for _, fe := range ve.Errors {
		if fe.Key == key {
			return nil, fe
		}
	}
	return ve, nil
}

func splitList(text string) []string {
	out := []string{}
	for _, part := range strings.Split(text, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// editDistance is the Levenshtein distance, for "did you mean" suggestions.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// SetValue assigns a JSON-typed value (string, bool, float64, []any) to a key,
// for importers that already hold typed data. Type mismatches are errors that
// name the key; lists accept only string items.
func (c *Config) SetValue(key string, value any) error {
	r, ok := c.find(key)
	if !ok {
		return &UnknownKeyError{Key: key, Suggestions: suggest(key)}
	}
	mismatch := func() error {
		return &FieldError{Key: key, Problem: fmt.Sprintf("expected %s, got %T", r.value.Kind(), value)}
	}
	switch r.value.Kind() {
	case reflect.String:
		switch v := value.(type) {
		case string:
			r.value.SetString(v)
		case float64:
			r.value.SetString(strconv.FormatFloat(v, 'f', -1, 64))
		case bool:
			r.value.SetString(strconv.FormatBool(v))
		case nil:
			r.value.SetString("")
		default:
			return mismatch()
		}
	case reflect.Bool:
		v, ok := value.(bool)
		if !ok {
			return mismatch()
		}
		r.value.SetBool(v)
	case reflect.Int:
		switch v := value.(type) {
		case float64:
			r.value.SetInt(int64(v))
		case int:
			r.value.SetInt(int64(v))
		case nil:
			r.value.SetInt(0)
		default:
			return mismatch()
		}
	case reflect.Slice:
		items, ok := value.([]any)
		if !ok {
			if value == nil {
				r.value.Set(reflect.ValueOf([]string{}))
				return nil
			}
			return mismatch()
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			str, ok := it.(string)
			if !ok {
				return &FieldError{Key: key, Problem: fmt.Sprintf("list items must be text, got %T", it)}
			}
			out = append(out, str)
		}
		r.value.Set(reflect.ValueOf(out))
	default:
		return fmt.Errorf("%s: unsupported type %s", key, r.value.Kind())
	}
	return nil
}
