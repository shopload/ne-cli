package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/shopload/ne-cli/internal/client"
	"github.com/shopload/ne-cli/internal/config"
	"github.com/shopload/ne-cli/internal/registry"
)

var tokenKeys = []string{"access_token", "access_token_end_date", "refresh_token", "refresh_token_end_date"}

// request is one API call, resolved from an endpoint or a raw path.
type request struct {
	Path   string
	Base   bool // send to the auth host (base.next-engine.org) instead of the API host
	Auth   bool // attach access_token / refresh_token
	Params map[string]string
	EP     *registry.Endpoint // nil for uncatalogued raw paths
}

func requestFor(ep *registry.Endpoint, params map[string]string) *request {
	return &request{Path: ep.Path, Base: ep.Host != "", Auth: ep.Auth, Params: params, EP: ep}
}

// send performs the request with the profile's tokens. It persists rotated
// tokens, removes them from the returned body, and maps failures to cliError.
// With --dry-run it returns the request description and sends nothing.
func send(ctx context.Context, r *request) (map[string]any, error) {
	store, err := config.Open(opts.profile)
	if err != nil {
		return nil, &cliError{Type: "config", Message: err.Error(), exit: exitUsage}
	}
	p := store.Profile
	params := map[string]string{}
	for k, v := range r.Params {
		params[k] = v
	}
	if opts.wait && params["wait_flag"] == "" && (r.EP == nil || r.EP.HasParam("wait_flag")) && r.Auth {
		params["wait_flag"] = "1"
	}
	// Auth endpoints take the app credentials instead of tokens.
	if r.EP != nil && !r.Auth {
		if r.EP.HasParam("client_id") && params["client_id"] == "" {
			params["client_id"] = p.ClientID
		}
		if r.EP.HasParam("client_secret") && params["client_secret"] == "" {
			params["client_secret"] = p.ClientSecret
		}
	}
	if !opts.noValidate && r.EP != nil {
		if issues := r.EP.Validate(params); len(issues) > 0 {
			return nil, &cliError{Type: "validation", Message: fmt.Sprintf("%d problem(s) in the request for %s", len(issues), r.EP.ID),
				Hint: "see details; run `ne-cli schema " + r.EP.ID + "` for the contract, or pass --no-validate to send anyway", Details: issues, exit: exitUsage}
		}
	}
	host := p.APIHost
	if r.Base {
		host = p.BaseHost
	}
	if opts.dryRun {
		shown := map[string]string{}
		for k, v := range params {
			if k == "client_secret" {
				v = "***"
			}
			shown[k] = v
		}
		if r.Auth {
			shown["access_token"], shown["refresh_token"] = "***", "***"
		}
		return map[string]any{"dry_run": true, "request": map[string]any{
			"method": "POST", "url": strings.TrimRight(host, "/") + r.Path,
			"content_type": "application/x-www-form-urlencoded", "params": shown}}, nil
	}
	if r.Auth {
		if p.AccessToken == "" {
			return nil, &cliError{Type: "auth", Message: "not logged in: no access token for profile " + store.Name,
				Hint: "run `ne-cli auth login`, or set NE_ACCESS_TOKEN and NE_REFRESH_TOKEN", exit: exitAuth}
		}
		params["access_token"] = p.AccessToken
		if p.RefreshToken != "" {
			params["refresh_token"] = p.RefreshToken
		}
	}
	sent := p.AccessToken
	body, err := client.New(opts.timeout).Post(ctx, host, r.Path, params)
	if body != nil {
		if at, _ := body["access_token"].(string); r.Auth && at != "" && at != sent {
			if serr := saveTokens(store, body, sent); serr != nil {
				fmt.Fprintln(os.Stderr, "warning: tokens were rotated but could not be saved:", serr)
			} else if store.TokensFromEnv {
				fmt.Fprintln(os.Stderr, "notice: tokens were rotated; the new pair is saved in", store.Path, "and takes precedence over NE_ACCESS_TOKEN")
			}
		}
		if r.Auth {
			for _, k := range tokenKeys {
				delete(body, k)
			}
		}
	}
	if err != nil {
		return nil, mapAPIError(err)
	}
	return body, nil
}

func saveTokens(store *config.Store, body map[string]any, rotatedFrom string) error {
	str := func(k string) string { s, _ := body[k].(string); return s }
	return store.Update(func(p *config.Profile) {
		p.AccessToken = str("access_token")
		p.AccessTokenEndDate = str("access_token_end_date")
		if rt := str("refresh_token"); rt != "" {
			p.RefreshToken = rt
			p.RefreshTokenEndDate = str("refresh_token_end_date")
		}
		p.RotatedFrom = rotatedFrom
	})
}

func mapAPIError(err error) error {
	ae, ok := err.(*client.APIError)
	if !ok {
		return err
	}
	ce := &cliError{Type: "api_error", Code: ae.Code, Message: ae.Message, exit: exitAPI}
	if m := registry.FindMessage(ae.Code); m != nil {
		ce.Retry = m.Retry
		ce.Hint = m.Note
	}
	switch {
	case ae.Result == "redirect" || ae.Code == "002002" || ae.Code == "002004":
		ce.Type, ce.exit = "auth", exitAuth
		ce.Hint = "the session expired; run `ne-cli auth login` again"
	case ae.Code == "003001" || ae.Code == "003002":
		ce.Retry = "yes"
		ce.Hint = "Next Engine is busy; retry later, or pass --wait to queue server-side (wait_flag=1)"
	case ae.Code == "004002" && ce.Hint == "":
		ce.Hint = "invalid search parameter; check field names and operator/type pairs with `ne-cli schema <id>`"
	}
	return ce
}

// ---- parameter assembly -------------------------------------------------

// readValue resolves "@path" to the file's contents and "-" to stdin.
func readValue(v string, stdin io.Reader) (string, error) {
	switch {
	case v == "-":
		b, err := io.ReadAll(stdin)
		return string(b), err
	case strings.HasPrefix(v, "@"):
		b, err := os.ReadFile(v[1:])
		if err != nil {
			return "", usageErr("cannot read %s: %v", v[1:], err)
		}
		return string(b), nil
	}
	return v, nil
}

// mergeParamsJSON adds the keys of a --params JSON object to dst. Scalars
// become strings, lists of scalars become a comma-separated value, and any
// other structure is sent as a JSON string.
func mergeParamsJSON(dst map[string]string, raw string, stdin io.Reader) error {
	if raw == "" {
		return nil
	}
	text, err := readValue(raw, stdin)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return usageErr("--params must be a JSON object: %v", err)
	}
	for k, v := range obj {
		dst[k] = formValue(v)
	}
	return nil
}

func formValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "1"
		}
		return "0"
	case []any:
		items := make([]string, 0, len(t))
		for _, it := range t {
			switch s := it.(type) {
			case string:
				items = append(items, s)
			case json.Number:
				items = append(items, s.String())
			default:
				b, _ := json.Marshal(v)
				return string(b)
			}
		}
		return joinList(items)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// joinList builds a comma-separated value, quoting items the way the API
// requires when they contain a comma, a double quote or a newline.
func joinList(items []string) string {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write(items)
	w.Flush()
	return strings.TrimRight(sb.String(), "\n")
}

// mergeKV adds repeated -p key=value pairs. "key" alone is allowed for the
// operators that take no value (-blank, -nblank, -null, -nnull).
func mergeKV(dst map[string]string, pairs []string, stdin io.Reader) error {
	for _, pair := range pairs {
		k, v, found := strings.Cut(pair, "=")
		if !found {
			_, op, ok := registry.SplitCondition(k)
			if !ok || (op != "blank" && op != "nblank" && op != "null" && op != "nnull") {
				return usageErr("-p expects key=value, got %q", pair)
			}
			v = "1"
		}
		val, err := readValue(v, stdin)
		if err != nil {
			return err
		}
		dst[strings.TrimSpace(k)] = val
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
