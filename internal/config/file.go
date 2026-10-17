package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	defaultConfigFile = "config.yaml" // read from the working directory when CONFIG_FILE is unset
	maxConfigFileSize = 1 << 20
)

var (
	utf8BOM      = []byte{0xEF, 0xBB, 0xBF}
	durationType = reflect.TypeFor[time.Duration]()
)

// loadFile applies the optional config file named by CONFIG_FILE onto cfg. A missing default
// file means defaults; a missing explicit CONFIG_FILE is an error.
func loadFile(cfg *Config, getenv func(string) string) error {
	path := strings.TrimSpace(getenv("CONFIG_FILE"))
	explicit := path != ""
	if !explicit {
		path = defaultConfigFile
	}
	data, err := readConfigFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist) && !explicit:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("CONFIG_FILE %s: file not found", path)
	default:
		return fmt.Errorf("config file %s: %w", path, err)
	}
	if err := decodeFile(cfg, data); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	return nil
}

func readConfigFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(data) > maxConfigFileSize {
		return nil, fmt.Errorf("larger than %d bytes", maxConfigFileSize)
	}
	return data, nil
}

// decodeFile strictly decodes one YAML document onto cfg. Keys left out, and empty sections,
// keep the values already in cfg. Unknown keys and wrong types are errors that name the
// dotted key. Errors never quote a value from the file, in case a secret was pasted into the
// wrong key.
func decodeFile(cfg *Config, data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, utf8BOM)))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // empty or comment-only
		}
		return fmt.Errorf("invalid YAML: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("want a single YAML document")
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil
		}
		root = root.Content[0]
	}
	return decodeValue(root, reflect.ValueOf(cfg).Elem(), "")
}

// decodeValue decodes n into v, the field at the dotted key. A null value keeps v as it is.
func decodeValue(n *yaml.Node, v reflect.Value, key string) error {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" {
		return nil
	}
	if v.Type() == durationType {
		d, err := time.ParseDuration(scalar(n))
		if err != nil {
			return typeError(n, key, "a duration such as 5s, 1m30s or 15m")
		}
		v.SetInt(int64(d))
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		return decodeStruct(n, v, key)
	case reflect.Map:
		return decodeWeights(n, v, key)
	case reflect.Int:
		var i int
		if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int" || n.Decode(&i) != nil {
			return typeError(n, key, "a whole number")
		}
		v.SetInt(int64(i))
	case reflect.Float64:
		var f float64
		if !isNumber(n) || n.Decode(&f) != nil {
			return typeError(n, key, "a number")
		}
		v.SetFloat(f)
	case reflect.String:
		if n.Kind != yaml.ScalarNode {
			return typeError(n, key, "a single value")
		}
		v.SetString(strings.TrimSpace(n.Value))
	default:
		return fmt.Errorf("%s: unsupported setting type %s", key, v.Type())
	}
	return nil
}

func decodeStruct(n *yaml.Node, v reflect.Value, key string) error {
	if n.Kind != yaml.MappingNode {
		if key == "" {
			return typeError(n, "file", "a mapping of sections such as pacing: or ai:")
		}
		return typeError(n, key, "a section of keys")
	}
	fields := map[string]int{}
	for i := range v.NumField() {
		if name := yamlName(v.Type().Field(i)); name != "" {
			fields[name] = i
		}
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, val := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return typeError(k, join(key, "?"), "a key name")
		}
		child := join(key, k.Value)
		if seen[k.Value] {
			return fmt.Errorf("%s: set twice (line %d)", child, k.Line)
		}
		seen[k.Value] = true
		idx, ok := fields[k.Value]
		switch {
		case !ok && child == "ai.api_key":
			return fmt.Errorf("%s: not allowed in the file (line %d); put the key in the environment variable named by ai.api_key_env (default AI_API_KEY)", child, k.Line)
		case !ok:
			return fmt.Errorf("%s: unknown key (line %d)", child, k.Line)
		}
		if err := decodeValue(val, v.Field(idx), child); err != nil {
			return err
		}
	}
	return nil
}

// decodeWeights adds the signal → weight entries of a mapping to the map in v.
func decodeWeights(n *yaml.Node, v reflect.Value, key string) error {
	if n.Kind != yaml.MappingNode {
		return typeError(n, key, "a mapping of signal names to weights")
	}
	if v.IsNil() {
		v.Set(reflect.MakeMap(v.Type()))
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, val := n.Content[i], n.Content[i+1]
		name := strings.TrimSpace(k.Value)
		if k.Kind != yaml.ScalarNode || name == "" {
			return typeError(k, join(key, "?"), "a signal name")
		}
		child := join(key, name)
		if seen[name] {
			return fmt.Errorf("%s: set twice (line %d)", child, k.Line)
		}
		seen[name] = true
		if !isNumber(val) {
			return typeError(val, child, "a number")
		}
		f := reflect.New(v.Type().Elem()).Elem()
		if err := decodeValue(val, f, child); err != nil {
			return err
		}
		v.SetMapIndex(reflect.ValueOf(name), f)
	}
	return nil
}

func yamlName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	if name == "-" {
		return ""
	}
	return name
}

func isNumber(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && (n.ShortTag() == "!!int" || n.ShortTag() == "!!float")
}

func scalar(n *yaml.Node) string {
	if n.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

func typeError(n *yaml.Node, key, want string) error {
	return fmt.Errorf("%s: want %s (line %d)", key, want, n.Line)
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
