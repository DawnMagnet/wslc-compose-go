package wslc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dawnmagnet/wslc-compose-go/internal/labels"
)

// Container is the subset of wslc list/inspect output wslc-compose relies on.
// wslc's inspect output follows an OCI-like schema rather than Docker's, so
// parsing is deliberately tolerant: it probes several well-known key paths.
type Container struct {
	ID       string
	Name     string
	Image    string
	State    string // running, exited, created, ...
	Health   string // healthy, unhealthy, starting or "" when unknown
	ExitCode int
	Ports    string
	Labels   map[string]string
}

// Running reports whether the container is up.
func (c Container) Running() bool { return strings.EqualFold(c.State, "running") }

// Service returns the compose service label.
func (c Container) Service() string { return c.Labels[labels.Service] }

// Hash returns the config-hash label.
func (c Container) Hash() string { return c.Labels[labels.ConfigHash] }

// Number returns the container-number label (0 for one-offs or unknown).
func (c Container) Number() int {
	n, _ := strconv.Atoi(c.Labels[labels.Number])
	return n
}

// OneOff reports whether the container was created by `wslc-compose run`.
func (c Container) OneOff() bool { return strings.EqualFold(c.Labels[labels.OneOff], "true") }

// ParseContainers decodes a JSON array, a single object, or newline-delimited
// JSON objects into containers.
func ParseContainers(data []byte) ([]Container, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var objs []map[string]any
	switch data[0] {
	case '[':
		if err := json.Unmarshal(data, &objs); err != nil {
			return nil, fmt.Errorf("decode wslc output: %w", err)
		}
	default:
		dec := json.NewDecoder(bytes.NewReader(data))
		for dec.More() {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				return nil, fmt.Errorf("decode wslc output: %w", err)
			}
			objs = append(objs, m)
		}
	}
	out := make([]Container, 0, len(objs))
	for _, m := range objs {
		out = append(out, parseContainer(m))
	}
	return out, nil
}

func parseContainer(m map[string]any) Container {
	c := Container{
		ID:     str(find(m, "Id"), find(m, "ID")),
		Name:   strings.TrimPrefix(str(find(m, "Name"), find(m, "Names")), "/"),
		Image:  str(find(m, "Image"), find(m, "Config", "Image")),
		Labels: labelMap(find(m, "Labels"), find(m, "Config", "Labels"), find(m, "Annotations")),
	}
	switch st := find(m, "State").(type) {
	case string:
		c.State = strings.ToLower(st)
	case map[string]any:
		c.State = strings.ToLower(str(find(st, "Status")))
		if c.State == "" {
			if r, _ := find(st, "Running").(bool); r {
				c.State = "running"
			} else if find(st, "Running") != nil {
				c.State = "exited"
			}
		}
		c.Health = strings.ToLower(str(find(st, "Health", "Status"), find(st, "Health")))
		if f, ok := find(st, "ExitCode").(float64); ok {
			c.ExitCode = int(f)
		}
	}
	if c.State == "" {
		c.State = strings.ToLower(str(find(m, "Status")))
		if strings.HasPrefix(c.State, "up") {
			c.State = "running"
		}
	}
	if c.Health == "" {
		c.Health = strings.ToLower(str(find(m, "Health", "Status"), find(m, "Health")))
	}
	c.Ports = ports(find(m, "Ports"))
	return c
}

// find walks a case-insensitive key path through nested JSON objects.
func find(m map[string]any, path ...string) any {
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = nil
		for key, v := range obj {
			if strings.EqualFold(key, k) {
				cur = v
				break
			}
		}
	}
	return cur
}

// str returns the first value that renders as a non-empty string.
func str(vs ...any) string {
	for _, v := range vs {
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		case []any:
			if len(t) > 0 {
				if s, ok := t[0].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

func labelMap(vs ...any) map[string]string {
	out := map[string]string{}
	for _, v := range vs {
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				if s, ok := val.(string); ok {
					out[k] = s
				}
			}
		case string:
			for _, kv := range strings.Split(t, ",") {
				if k, val, ok := strings.Cut(kv, "="); ok {
					out[strings.TrimSpace(k)] = val
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

func ports(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, p := range t {
			m, ok := p.(map[string]any)
			if !ok {
				continue
			}
			host := str(find(m, "BindingAddress"), find(m, "HostIp"), find(m, "IP"))
			if host == "" {
				host = "0.0.0.0"
			}
			proto := strings.ToLower(fmt.Sprint(firstNonNil(find(m, "Protocol"), find(m, "Type"), "tcp")))
			switch proto {
			case "6":
				proto = "tcp"
			case "17":
				proto = "udp"
			}
			parts = append(parts, fmt.Sprintf("%s:%v->%v/%s", host,
				num(firstNonNil(find(m, "HostPort"), find(m, "PublicPort"))),
				num(firstNonNil(find(m, "ContainerPort"), find(m, "PrivatePort"))), proto))
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func num(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// parseNames extracts names from `wslc network|volume list` output, which may
// be JSON or a table whose name column header contains "NAME".
func parseNames(data []byte) []string {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if data[0] == '[' || data[0] == '{' {
		var names []string
		dec := json.NewDecoder(bytes.NewReader(data))
		for dec.More() {
			var v any
			if dec.Decode(&v) != nil {
				break
			}
			items, ok := v.([]any)
			if !ok {
				items = []any{v}
			}
			for _, it := range items {
				if m, ok := it.(map[string]any); ok {
					if n := str(find(m, "Name")); n != "" {
						names = append(names, n)
					}
				}
			}
		}
		return names
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	col := -1
	for _, h := range []string{"VOLUME NAME", "NAME"} {
		if col = strings.Index(lines[0], h); col >= 0 {
			break
		}
	}
	if col < 0 {
		return nil
	}
	var names []string
	for _, l := range lines[1:] {
		if len(l) > col {
			if f := strings.Fields(l[col:]); len(f) > 0 {
				names = append(names, f[0])
			}
		}
	}
	return names
}
