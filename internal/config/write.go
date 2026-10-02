package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

const fileHeader = `aja configuration. Edit by hand or with "aja config set <section.key> <value>".
Secrets (API key, Telegram) are NOT here: put them in the .env file next to this one
or in the environment. See "aja doctor" for the variable names and every path in use.`

// Marshal renders the config as YAML with every section and key commented from
// the struct tags. The same writer serves init and config set, so comments
// survive edits made through the tool.
func Marshal(c Config) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, HeadComment: fileHeader}
	v := reflect.ValueOf(c)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: yamlName(sf), HeadComment: "\n" + sf.Tag.Get("doc")}
		valNode, err := structNode(v.Field(i))
		if err != nil {
			return nil, err
		}
		root.Content = append(root.Content, keyNode, valNode)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

func structNode(v reflect.Value) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: yamlName(sf), HeadComment: comment(sf)}
		var val *yaml.Node
		var err error
		if sf.Type.Kind() == reflect.Struct {
			val, err = structNode(v.Field(i))
		} else {
			val = &yaml.Node{}
			err = val.Encode(v.Field(i).Interface())
			if sf.Type.Kind() == reflect.Slice && v.Field(i).Len() == 0 {
				val.Style = yaml.FlowStyle
			}
		}
		if err != nil {
			return nil, err
		}
		node.Content = append(node.Content, key, val)
	}
	return node, nil
}

func comment(sf reflect.StructField) string {
	parts := []string{sf.Tag.Get("doc")}
	if opts := options(sf); opts != nil {
		quoted := make([]string, 0, len(opts))
		for _, o := range opts {
			quoted = append(quoted, fmt.Sprintf("%q", o))
		}
		parts = append(parts, "Options: "+strings.Join(quoted, ", "))
	}
	if ex := sf.Tag.Get("example"); ex != "" {
		parts = append(parts, "Example: "+ex)
	}
	return strings.Join(parts, "\n")
}

// Write marshals the config to its file atomically (temp file + rename),
// creating the directory when needed.
func Write(path string, c Config) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
