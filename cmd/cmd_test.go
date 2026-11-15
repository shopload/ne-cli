package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeNE is a stand-in for the Next Engine API.
type fakeNE struct {
	*httptest.Server
	calls   []url.Values // form of every request, in order
	paths   []string
	handler func(path string, form url.Values) map[string]any
}

func newFake(t *testing.T, handler func(path string, form url.Values) map[string]any) *fakeNE {
	t.Helper()
	f := &fakeNE{handler: handler}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("want form POST, got %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		f.calls = append(f.calls, r.PostForm)
		f.paths = append(f.paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(f.handler(r.URL.Path, r.PostForm))
	}))
	t.Cleanup(f.Close)
	dir := t.TempDir()
	t.Setenv("NE_CLI_CONFIG_DIR", dir)
	t.Setenv("NE_API_HOST", f.URL)
	t.Setenv("NE_BASE_HOST", f.URL)
	for _, k := range []string{"NE_ACCESS_TOKEN", "NE_REFRESH_TOKEN", "NE_CLIENT_ID", "NE_CLIENT_SECRET", "NE_REDIRECT_URI", "NE_PROFILE"} {
		t.Setenv(k, "")
	}
	return f
}

func login(t *testing.T) {
	t.Helper()
	runCLI(t, `{"access_token":"at1","refresh_token":"rt1"}`, "auth", "import")
}

type result struct {
	code   int
	stdout string
	stderr string
}

func (r result) json(t *testing.T) map[string]any {
	t.Helper()
	src := r.stdout
	if r.code != 0 {
		src = r.stderr
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		t.Fatalf("output is not JSON: %v\nstdout=%s\nstderr=%s", err, r.stdout, r.stderr)
	}
	return v
}

func runCLI(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(stdin), &out, &errb)
	return result{code, out.String(), errb.String()}
}

func errorOf(t *testing.T, r result) map[string]any {
	t.Helper()
	v := r.json(t)
	if v["ok"] != false {
		t.Fatalf("want ok=false, got %v", v)
	}
	return v["error"].(map[string]any)
}

func TestSearchSendsFormAndHidesTokens(t *testing.T) {
	f := newFake(t, func(path string, form url.Values) map[string]any {
		return map[string]any{"result": "success", "count": "1", "data": []any{map[string]any{"goods_id": "A1", "goods_name": "りんご"}},
			"access_token": "at1", "refresh_token": "rt1"}
	})
	login(t)
	r := runCLI(t, "", "master", "goods", "search", "--fields", "goods_id,goods_name", "-p", "goods_id-like=A%", "--wait")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	form := f.calls[0]
	if f.paths[0] != "/api_v1_master_goods/search" || form.Get("access_token") != "at1" || form.Get("refresh_token") != "rt1" ||
		form.Get("goods_id-like") != "A%" || form.Get("fields") != "goods_id,goods_name" || form.Get("wait_flag") != "1" {
		t.Errorf("unexpected request %s %v", f.paths[0], form)
	}
	if strings.Contains(r.stdout, "at1") || strings.Contains(r.stdout, "refresh_token") {
		t.Errorf("tokens leaked to stdout: %s", r.stdout)
	}
	if v := r.json(t); v["ok"] != true || v["count"] != "1" {
		t.Errorf("unexpected envelope %v", v)
	}
}

func TestRotatedTokensAreSavedAndUsed(t *testing.T) {
	f := newFake(t, func(path string, form url.Values) map[string]any {
		return map[string]any{"result": "success", "data": []any{}, "access_token": "at2", "refresh_token": "rt2",
			"access_token_end_date": "2099-01-01 00:00:00", "refresh_token_end_date": "2099-01-03 00:00:00"}
	})
	login(t)
	runCLI(t, "", "login", "user", "info")
	runCLI(t, "", "login", "user", "info")
	if got := f.calls[1].Get("access_token") + "/" + f.calls[1].Get("refresh_token"); got != "at2/rt2" {
		t.Errorf("second call used %s, want the rotated pair", got)
	}
	// A stale token in the environment must not override the rotated pair.
	t.Setenv("NE_ACCESS_TOKEN", "at1")
	runCLI(t, "", "login", "user", "info")
	if got := f.calls[2].Get("access_token"); got != "at2" {
		t.Errorf("stale env token won: %s", got)
	}
	st, _ := os.Stat(filepath.Join(os.Getenv("NE_CLI_CONFIG_DIR"), "config.json"))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("config file mode %v, want 0600", st.Mode().Perm())
	}
}

func TestAPIErrorMapping(t *testing.T) {
	reply := map[string]any{}
	newFake(t, func(string, url.Values) map[string]any { return reply })
	login(t)

	reply = map[string]any{"result": "redirect", "code": "002004", "message": "access_tokenの有効期限が終了しました。"}
	r := runCLI(t, "", "login", "user", "info")
	if e := errorOf(t, r); r.code != exitAuth || e["type"] != "auth" || e["code"] != "002004" {
		t.Errorf("redirect: exit %d %v", r.code, e)
	}
	reply = map[string]any{"result": "error", "code": "003002", "message": "busy"}
	r = runCLI(t, "", "login", "user", "info")
	if e := errorOf(t, r); r.code != exitAPI || e["retryable"] != "yes" {
		t.Errorf("busy: exit %d %v", r.code, e)
	}
}

func TestValidationRejectsBeforeSending(t *testing.T) {
	f := newFake(t, func(string, url.Values) map[string]any { return map[string]any{"result": "success"} })
	login(t)
	n := len(f.calls)
	r := runCLI(t, "", "receiveorder", "base", "search", "--fields", "receive_order_id,nope",
		"-p", "receive_order_order_status_id-lte=20", "-p", "receive_order_date-like=2026%")
	e := errorOf(t, r)
	if r.code != exitUsage || e["type"] != "validation" || len(e["details"].([]any)) != 3 {
		t.Errorf("exit %d %v", r.code, e)
	}
	if len(f.calls) != n {
		t.Error("an invalid request reached the API")
	}
	if r := runCLI(t, "", "receiveorder", "base", "search", "--fields", "nope", "--no-validate"); r.code != 0 {
		t.Errorf("--no-validate should send anyway: %s", r.stderr)
	}
	if r := runCLI(t, "", "receiveorder", "base", "search"); errorOf(t, r)["type"] != "validation" {
		t.Error("missing required fields param was not reported")
	}
}

func TestHighRiskNeedsYes(t *testing.T) {
	f := newFake(t, func(string, url.Values) map[string]any { return map[string]any{"result": "success"} })
	login(t)
	args := []string{"receiveorder", "base", "shipped", "--receive-order-id", "1", "--receive-order-last-modified-date", "2026-01-01 00:00:00"}
	n := len(f.calls)
	if r := runCLI(t, "", args...); r.code != exitUsage || errorOf(t, r)["type"] != "confirmation_required" || len(f.calls) != n {
		t.Errorf("shipped ran without --yes: %d", r.code)
	}
	if r := runCLI(t, "", append(args, "--dry-run")...); r.code != 0 || len(f.calls) != n {
		t.Errorf("--dry-run must not send: %d %s", r.code, r.stderr)
	}
	if r := runCLI(t, "", append(args, "--yes")...); r.code != 0 || len(f.calls) != n+1 {
		t.Errorf("--yes should send: %d %s", r.code, r.stderr)
	}
}

func TestPageAll(t *testing.T) {
	f := newFake(t, func(path string, form url.Values) map[string]any {
		off, _ := strconv.Atoi(form.Get("offset"))
		lim, _ := strconv.Atoi(form.Get("limit"))
		rows := []any{}
		for i := off; i < off+lim && i < 5; i++ {
			rows = append(rows, map[string]any{"goods_id": "G" + strconv.Itoa(i)})
		}
		return map[string]any{"result": "success", "count": strconv.Itoa(len(rows)), "data": rows}
	})
	login(t)
	r := runCLI(t, "", "master", "goods", "search", "--fields", "goods_id", "--page-all", "--page-size", "2")
	v := r.json(t)
	if len(v["data"].([]any)) != 5 || v["has_more"] != false || len(f.calls) != 3 {
		t.Errorf("rows=%d calls=%d %v", len(v["data"].([]any)), len(f.calls), v["has_more"])
	}
	r = runCLI(t, "", "master", "goods", "search", "--fields", "goods_id", "--page-all", "--page-size", "2", "--page-limit", "1")
	if v := r.json(t); v["has_more"] != true || v["next_offset"] != float64(2) {
		t.Errorf("page-limit: %v", v)
	}
	r = runCLI(t, "", "master", "goods", "search", "--fields", "goods_id", "--page-all", "--page-size", "2", "--limit", "3", "--format", "csv")
	if r.stdout != "goods_id\nG0\nG1\nG2\n" {
		t.Errorf("csv with limit: %q", r.stdout)
	}
}

func TestOrderListWithRows(t *testing.T) {
	f := newFake(t, func(path string, form url.Values) map[string]any {
		if strings.Contains(path, "_row/") {
			if form.Get("offset") != "0" {
				return map[string]any{"result": "success", "data": []any{}}
			}
			return map[string]any{"result": "success", "data": []any{
				map[string]any{"receive_order_row_receive_order_id": "10", "receive_order_row_goods_id": "A"},
				map[string]any{"receive_order_row_receive_order_id": "10", "receive_order_row_goods_id": "B"}}}
		}
		return map[string]any{"result": "success", "count": "2", "data": []any{
			map[string]any{"receive_order_id": "10"}, map[string]any{"receive_order_id": "11"}}}
	})
	login(t)
	r := runCLI(t, "", "receiveorder", "+list", "--status", "20,40", "--to", "2026-11-15", "--with-rows")
	if r.code != 0 {
		t.Fatal(r.stderr)
	}
	head := f.calls[0]
	if head.Get("receive_order_cancel_type_id-eq") != "0" || head.Get("receive_order_order_status_id-in") != "20,40" ||
		head.Get("receive_order_date-lte") != "2026-11-15 23:59:59" {
		t.Errorf("header conditions: %v", head)
	}
	if got := f.calls[1].Get("receive_order_row_receive_order_id-in"); got != "10,11" {
		t.Errorf("row search keyed by %q", got)
	}
	data := r.json(t)["data"].([]any)
	if len(data[0].(map[string]any)["rows"].([]any)) != 2 || len(data[1].(map[string]any)["rows"].([]any)) != 0 {
		t.Errorf("rows not grouped: %v", data)
	}
}

func TestOrderUpdateLooksUpLockAndBuildsXML(t *testing.T) {
	f := newFake(t, func(path string, form url.Values) map[string]any {
		if strings.HasSuffix(path, "/search") {
			return map[string]any{"result": "success", "data": []any{map[string]any{"receive_order_id": "7", "receive_order_last_modified_date": "2026-11-01 10:00:00"}}}
		}
		return map[string]any{"result": "success"}
	})
	login(t)
	r := runCLI(t, "", "receiveorder", "+update", "--id", "7", "--set", "receive_order_note=a<b", "--row-set", "2:receive_order_row_quantity=3")
	if r.code != 0 {
		t.Fatal(r.stderr)
	}
	up := f.calls[1]
	want := `<?xml version="1.0" encoding="utf-8"?><root><receiveorder_base><receive_order_note>a&lt;b</receive_order_note></receiveorder_base>` +
		`<receiveorder_row><receive_order_row_no value="2"><receive_order_row_quantity>3</receive_order_row_quantity></receive_order_row_no></receiveorder_row></root>`
	if up.Get("data") != want || up.Get("receive_order_last_modified_date") != "2026-11-01 10:00:00" || up.Get("receive_order_id") != "7" {
		t.Errorf("update request: %v", up)
	}
	if r := runCLI(t, "", "receiveorder", "+update", "--id", "7", "--set", "receive_order_id=9"); errorOf(t, r)["type"] != "validation" {
		t.Error("a non-updatable field was accepted")
	}
}

func TestLoginExchange(t *testing.T) {
	f := newFake(t, func(path string, form url.Values) map[string]any {
		return map[string]any{"result": "success", "access_token": "atX", "refresh_token": "rtX", "company_name": "テスト社", "pic_name": "山田",
			"access_token_end_date": "2099-01-01 00:00:00", "refresh_token_end_date": "2099-01-03 00:00:00"}
	})
	if r := runCLI(t, "s3cret\n", "config", "init", "--client-id", "cid", "--client-secret-stdin", "--redirect-uri", "https://example.com/cb"); r.code != 0 || strings.Contains(r.stdout, "s3cret") {
		t.Fatalf("config init: %d %s %s", r.code, r.stdout, r.stderr)
	}
	r := runCLI(t, "", "auth", "login", "--no-wait")
	if u, _ := r.json(t)["url"].(string); !strings.Contains(u, "/users/sign_in/?client_id=cid&redirect_uri=https%3A%2F%2Fexample.com%2Fcb") {
		t.Errorf("login url: %s", r.stdout)
	}
	r = runCLI(t, "", "auth", "login", "--callback-url", "https://example.com/cb?uid=U&state=S")
	form := f.calls[0]
	if f.paths[0] != "/api_neauth" || form.Get("uid") != "U" || form.Get("state") != "S" || form.Get("client_secret") != "s3cret" {
		t.Errorf("exchange request: %s %v", f.paths[0], form)
	}
	v := r.json(t)
	if v["logged_in"] != true || v["company_name"] != "テスト社" || strings.Contains(r.stdout, "atX") {
		t.Errorf("login result: %s", r.stdout)
	}
	runCLI(t, "", "login", "company", "info")
	if f.calls[1].Get("access_token") != "atX" {
		t.Error("stored token was not used")
	}
	if r := runCLI(t, "", "auth", "logout"); r.code != 0 {
		t.Fatal(r.stderr)
	}
	if r := runCLI(t, "", "login", "company", "info"); r.code != exitAuth {
		t.Errorf("after logout: exit %d", r.code)
	}
}

func TestRawAPIAndParamsJSON(t *testing.T) {
	f := newFake(t, func(string, url.Values) map[string]any { return map[string]any{"result": "success", "count": "3"} })
	login(t)
	r := runCLI(t, "", "api", "POST", "api_v1_receiveorder_base/count", "--params", `{"receive_order_order_status_id-in":["20","a,b"],"receive_order_shop_id-eq":3}`)
	if r.code != 0 {
		t.Fatal(r.stderr)
	}
	form := f.calls[0]
	if form.Get("receive_order_order_status_id-in") != `20,"a,b"` || form.Get("receive_order_shop_id-eq") != "3" {
		t.Errorf("params: %v", form)
	}
	if r := runCLI(t, "", "api", "/api_v1_future/thing", "-p", "x=1"); r.code != 0 || f.paths[1] != "/api_v1_future/thing" || f.calls[1].Get("access_token") != "at1" {
		t.Errorf("uncatalogued path: %d %v", r.code, f.paths)
	}
	if r := runCLI(t, "", "api", "GET", "/x"); r.code != exitUsage {
		t.Error("GET should be rejected")
	}
}

func TestSchemaIsOfflineAndComplete(t *testing.T) {
	t.Setenv("NE_CLI_CONFIG_DIR", t.TempDir())
	v := runCLI(t, "", "schema").json(t)
	if v["count"] != float64(131) {
		t.Errorf("catalog has %v endpoints, want 131", v["count"])
	}
	v = runCLI(t, "", "schema", "/api_v1_receiveorder_base/update").json(t)
	if v["id"] != "receiveorder.base.update" || len(v["data_fields"].([]any)) == 0 {
		t.Errorf("schema: %v", v["id"])
	}
	if r := runCLI(t, "", "schema", "errors", "004002", "--format", "table"); !strings.Contains(r.stdout, "004002") {
		t.Errorf("errors table: %s", r.stdout)
	}
}
