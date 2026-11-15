// Package registry holds the embedded catalog of Next Engine API endpoints.
//
// meta.json is generated from the official OpenAPI spec by scripts/gen_meta.py.
package registry

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

//go:embed meta.json
var metaJSON []byte

type Param struct {
	Name     string `json:"name"`
	Desc     string `json:"desc"`
	Required bool   `json:"required"`
}

type Field struct {
	Name  string `json:"name"`
	Desc  string `json:"desc"`
	Type  string `json:"type,omitempty"`
	Table string `json:"table,omitempty"`
}

type DataField struct {
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Section string `json:"section,omitempty"`
}

type Endpoint struct {
	ID         string      `json:"id"`
	Path       string      `json:"path"`
	Service    string      `json:"service"`
	Resource   string      `json:"resource"`
	Method     string      `json:"method"`
	Tag        string      `json:"tag"`
	Summary    string      `json:"summary"`
	DocURL     string      `json:"doc_url"`
	Host       string      `json:"host,omitempty"`
	Auth       bool        `json:"auth"`
	Filterable bool        `json:"filterable"`
	Risk       string      `json:"risk"`
	Params     []Param     `json:"params"`
	Fields     []Field     `json:"fields,omitempty"`
	DataFields []DataField `json:"data_fields,omitempty"`
	Response   []Param     `json:"response,omitempty"`
}

type Message struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   string `json:"retry"`
	Note    string `json:"note"`
}

type Meta struct {
	APIHost   string      `json:"api_host"`
	Source    string      `json:"source"`
	Endpoints []*Endpoint `json:"endpoints"`
	Messages  []Message   `json:"messages"`
}

var (
	once sync.Once
	meta *Meta
)

// Load returns the embedded catalog.
func Load() *Meta {
	once.Do(func() {
		meta = &Meta{}
		if err := json.Unmarshal(metaJSON, meta); err != nil {
			panic("ne-cli: corrupt embedded meta.json: " + err.Error())
		}
	})
	return meta
}

// Find looks an endpoint up by id ("receiveorder.base.search") or by path
// ("/api_v1_receiveorder_base/search", leading slash optional).
func Find(key string) *Endpoint {
	key = strings.TrimSpace(key)
	path := "/" + strings.Trim(key, "/")
	for _, e := range Load().Endpoints {
		if e.ID == key || e.Path == path {
			return e
		}
	}
	return nil
}

// Services lists service names in catalog order.
func Services() []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range Load().Endpoints {
		if !seen[e.Service] {
			seen[e.Service] = true
			out = append(out, e.Service)
		}
	}
	return out
}

// FindMessage returns the documented entry for an API message code.
func FindMessage(code string) *Message {
	for i := range Load().Messages {
		if Load().Messages[i].Code == code {
			return &Load().Messages[i]
		}
	}
	return nil
}

// Field returns the named field of the endpoint, or nil.
func (e *Endpoint) Field(name string) *Field {
	for i := range e.Fields {
		if e.Fields[i].Name == name {
			return &e.Fields[i]
		}
	}
	return nil
}

// HasParam reports whether the endpoint documents the named parameter.
func (e *Endpoint) HasParam(name string) bool {
	for _, p := range e.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// Suggest returns up to n field names that look like name.
func (e *Endpoint) Suggest(name string, n int) []string {
	type cand struct {
		name string
		dist int
	}
	var cs []cand
	for _, f := range e.Fields {
		d := levenshtein(name, f.Name)
		if strings.Contains(f.Name, name) || strings.Contains(name, f.Name) {
			d = min(d, 2)
		}
		if d <= max(3, len(name)/3) {
			cs = append(cs, cand{f.Name, d})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].dist < cs[j].dist })
	var out []string
	for i := 0; i < len(cs) && i < n; i++ {
		out = append(out, cs[i].name)
	}
	return out
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
