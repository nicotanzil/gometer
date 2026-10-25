// Package sharedtest validates documents whose public fields may be shared with
// all workspace members. It never reads or contains resolved secret values.
package sharedtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"gometer/internal/config"
)

const MaxBindings = 1024

var ErrInvalid = errors.New("invalid shared test; use public configuration and secret references with empty destination fields")

type Binding struct {
	Path        string `json:"path"`
	ReferenceID string `json:"reference_id"`
	Revision    int64  `json:"revision"`
}

type Document struct {
	SchemaVersion  int             `json:"schema_version"`
	PublicConfig   json.RawMessage `json:"public_config"`
	SecretBindings []Binding       `json:"secret_bindings"`
}

// Parse rejects duplicate JSON keys as well as unknown envelope/configuration
// fields. A browser, database and executor must not interpret different values.
func Parse(raw []byte) (Document, config.Config, error) {
	var d Document
	if len(raw) > config.MaxConfigBytes || !UnambiguousJSON(raw) {
		return d, config.Config{}, ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || d.SchemaVersion != 1 || len(d.SecretBindings) > MaxBindings {
		return Document{}, config.Config{}, ErrInvalid
	}
	c, err := config.Load(bytes.NewReader(d.PublicConfig))
	if err != nil {
		return Document{}, config.Config{}, ErrInvalid
	}
	if _, err = c.Validate(); err != nil {
		return Document{}, config.Config{}, ErrInvalid
	}
	var public map[string]any
	if json.Unmarshal(d.PublicConfig, &public) != nil {
		return Document{}, config.Config{}, ErrInvalid
	}
	paths := map[string]bool{}
	for _, b := range d.SecretBindings {
		parts, ok := pointer(b.Path)
		if !ok || !allowedPath(parts) || paths[b.Path] || !validID(b.ReferenceID) || b.Revision < 1 {
			return Document{}, config.Config{}, ErrInvalid
		}
		value, ok := lookup(public, parts)
		if !ok || value != "" {
			return Document{}, config.Config{}, ErrInvalid
		}
		paths[b.Path] = true
	}
	// Standard credential headers cannot be accidentally declared public. Other
	// application-specific sensitive fields require the author's classification.
	if !credentialHeadersBound(c, paths) {
		return Document{}, config.Config{}, ErrInvalid
	}
	if d.SecretBindings == nil {
		d.SecretBindings = []Binding{}
	}
	return d, c, nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func pointer(path string) ([]string, bool) {
	if len(path) < 2 || len(path) > 2048 || path[0] != '/' {
		return nil, false
	}
	parts := strings.Split(path[1:], "/")
	for i, part := range parts {
		var out strings.Builder
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				out.WriteByte(part[j])
				continue
			}
			j++
			if j >= len(part) || part[j] != '0' && part[j] != '1' {
				return nil, false
			}
			if part[j] == '0' {
				out.WriteByte('~')
			} else {
				out.WriteByte('/')
			}
		}
		parts[i] = out.String()
	}
	return parts, true
}
func index(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0 && strconv.Itoa(n) == s
}
func allowedPath(p []string) bool {
	if len(p) == 1 && p[0] == "body" {
		return true
	}
	if len(p) == 2 && (p[0] == "variables" || p[0] == "headers") {
		return p[1] != "" && (p[0] != "headers" || !strings.EqualFold(p[1], "Host"))
	}
	if len(p) < 4 || p[0] != "scenarios" {
		return false
	}
	if _, ok := index(p[1]); !ok {
		return false
	}
	if len(p) == 4 && p[2] == "variables" {
		return p[3] != ""
	}
	if len(p) == 5 && p[2] == "data" {
		_, ok := index(p[3])
		return ok && p[4] != ""
	}
	return stepPath(p[2:])
}
func stepPath(p []string) bool {
	if len(p) < 3 || p[0] != "steps" {
		return false
	}
	if _, ok := index(p[1]); !ok {
		return false
	}
	tail := p[2:]
	if len(tail) == 1 && tail[0] == "body" {
		return true
	}
	if len(tail) == 2 && tail[0] == "headers" {
		return tail[1] != "" && !strings.EqualFold(tail[1], "Host")
	}
	if len(tail) >= 4 && tail[0] == "loop" {
		return stepPath(tail[1:])
	}
	if len(tail) >= 4 && tail[0] == "if" && (tail[1] == "then" || tail[1] == "else") {
		return stepPath(append([]string{"steps"}, tail[2:]...))
	}
	return false
}
func lookup(value any, parts []string) (any, bool) {
	for _, p := range parts {
		switch v := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = v[p]
			if !ok {
				return nil, false
			}
		case []any:
			n, ok := index(p)
			if !ok || n >= len(v) {
				return nil, false
			}
			value = v[n]
		default:
			return nil, false
		}
	}
	return value, true
}
func escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func credentialHeadersBound(c config.Config, bindings map[string]bool) bool {
	check := func(headers map[string]string, path string) bool {
		names := map[string]bool{}
		for key, literal := range headers {
			lower := strings.ToLower(key)
			if names[lower] {
				return false
			}
			names[lower] = true
			switch lower {
			case "authorization", "proxy-authorization", "cookie", "x-api-key":
				if literal != "" || !bindings[path+"/headers/"+escape(key)] {
					return false
				}
			}
		}
		return true
	}
	var steps func([]config.Step, string) bool
	steps = func(nodes []config.Step, path string) bool {
		for i, node := range nodes {
			at := path + "/" + strconv.Itoa(i)
			if !check(node.Headers, at) {
				return false
			}
			if node.If != nil && (!steps(node.If.Then, at+"/if/then") || !steps(node.If.Else, at+"/if/else")) {
				return false
			}
			if node.Loop != nil && !steps(node.Loop.Steps, at+"/loop/steps") {
				return false
			}
		}
		return true
	}
	if !check(c.Headers, "") {
		return false
	}
	for i, scenario := range c.Scenarios {
		if !steps(scenario.Steps, "/scenarios/"+strconv.Itoa(i)+"/steps") {
			return false
		}
	}
	return true
}

// UnambiguousJSON rejects duplicate keys, case variants, trailing values and
// excessive nesting before decoding a typed document or its API envelope.
func UnambiguousJSON(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 64 {
			return false
		}
		token, err := dec.Token()
		if err != nil {
			return false
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return true
		}
		switch delim {
		case '{':
			names := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || names[strings.ToLower(name)] {
					return false
				}
				names[strings.ToLower(name)] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for dec.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}
