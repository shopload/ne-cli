package registry

import (
	"fmt"
	"strings"
)

// Operators accepted in "<field>-<operator>" search conditions, with the
// field types each one supports (empty = every type).
var operators = map[string][]string{
	"eq": nil, "neq": nil,
	"lt": {"number", "datetime"}, "lte": {"number", "datetime"},
	"gt": {"number", "datetime"}, "gte": {"number", "datetime"},
	"in": {"string", "number"}, "nin": {"string", "number"},
	"like": {"string"}, "nlike": {"string"}, "nlikeornull": {"string"},
	"blank": nil, "nblank": nil, "null": nil, "nnull": nil,
}

// Issue is one problem found in a request before it is sent.
type Issue struct {
	Param   string `json:"param"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// SplitCondition splits "goods_id-like" into ("goods_id", "like").
func SplitCondition(key string) (field, op string, ok bool) {
	i := strings.LastIndex(key, "-")
	if i <= 0 {
		return "", "", false
	}
	if _, known := operators[key[i+1:]]; !known {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// Validate checks params against what the catalog documents for e. It only
// reports problems the API would reject or silently mishandle.
func (e *Endpoint) Validate(params map[string]string) []Issue {
	var issues []Issue
	for _, p := range e.Params {
		if p.Required && params[p.Name] == "" {
			issues = append(issues, Issue{Param: p.Name, Message: "required parameter is missing", Hint: p.Desc})
		}
	}
	if !e.Filterable || len(e.Fields) == 0 {
		return issues
	}
	// 商品ページ系は演算子と型の対応が別仕様なので、型の検査はしない。
	checkTypes := !strings.HasPrefix(e.Resource, "goods_page")

	if e.HasParam("fields") {
		for _, name := range strings.Split(params["fields"], ",") {
			name = strings.TrimSpace(name)
			if name != "" && e.Field(name) == nil {
				issues = append(issues, Issue{Param: "fields", Message: fmt.Sprintf("unknown field %q", name), Hint: suggestHint(e, name)})
			}
		}
	}
	for key, value := range params {
		if e.HasParam(key) {
			continue
		}
		field, op, ok := SplitCondition(key)
		if !ok {
			issues = append(issues, Issue{Param: key, Message: "not a documented parameter or a <field>-<operator> condition",
				Hint: "operators: eq neq lt lte gt gte in nin like nlike nlikeornull blank nblank null nnull"})
			continue
		}
		f := e.Field(field)
		if f == nil {
			issues = append(issues, Issue{Param: key, Message: fmt.Sprintf("unknown field %q", field), Hint: suggestHint(e, field)})
			continue
		}
		if allowed := operators[op]; checkTypes && allowed != nil && f.Type != "" && !contains(allowed, f.Type) {
			hint := ""
			if f.Type == "string" && (op == "lt" || op == "lte" || op == "gt" || op == "gte") {
				hint = fmt.Sprintf("%s is a string-typed code even if it looks numeric; list the codes with %s-in=a,b,c", field, field)
			}
			issues = append(issues, Issue{Param: key, Message: fmt.Sprintf("operator -%s cannot be used on a %s field (API error 004002)", op, f.Type), Hint: hint})
		}
		if (op == "in" || op == "nin") && strings.Count(value, ",") >= 1000 {
			issues = append(issues, Issue{Param: key, Message: "-in/-nin accept at most 1000 values", Hint: "split the request into chunks"})
		}
	}
	return issues
}

func suggestHint(e *Endpoint, name string) string {
	if s := e.Suggest(name, 3); len(s) > 0 {
		return "did you mean: " + strings.Join(s, ", ") + "? (ne-cli schema " + e.ID + " lists every field)"
	}
	return "run: ne-cli schema " + e.ID
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
