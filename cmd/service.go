package cmd

import (
	"context"
	"encoding/json"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/shopload/ne-cli/internal/registry"
)

var serviceShort = map[string]string{
	"receiveorder": "受注 (orders): 伝票・明細・確認内容・オプション",
	"master":       "マスタ: 商品・在庫・店舗・仕入先・卸先・商品ページ",
	"warehouse":    "拠点 (warehouses): 拠点マスタ・拠点在庫",
	"supplyorder":  "仕入 (purchases): 仕入伝票・仕入明細",
	"placeorder":   "発注 (purchase orders): 発注伝票・発注明細",
	"system":       "区分情報・モール・アップロードキュー",
	"notice":       "実行結果お知らせ",
	"login":        "ログイン中の企業・ユーザー情報",
	"app":          "アプリ利用企業一覧",
	"neauth":       "認証 API (通常は `ne-cli auth login` を使う)",
}

// addServiceCommands builds `ne-cli <service> <resource> <method>` for every
// endpoint in the catalog, and attaches the shortcuts.
func addServiceCommands(root *cobra.Command) {
	services := map[string]*cobra.Command{}
	resources := map[string]*cobra.Command{}
	for _, ep := range registry.Load().Endpoints {
		svc := services[ep.Service]
		if svc == nil {
			svc = &cobra.Command{Use: ep.Service, Short: serviceShort[ep.Service], GroupID: "service"}
			svc.AddGroup(&cobra.Group{ID: "shortcut", Title: "Shortcuts:"}, &cobra.Group{ID: "resource", Title: "Resources:"})
			services[ep.Service] = svc
			root.AddCommand(svc)
		}
		key := ep.Service + "." + ep.Resource
		res := resources[key]
		if res == nil {
			res = &cobra.Command{Use: ep.Resource, Short: ep.Tag, GroupID: "resource"}
			resources[key] = res
			svc.AddCommand(res)
		}
		res.AddCommand(newMethodCmd(ep))
	}
	for service, build := range shortcuts {
		for _, c := range build() {
			c.GroupID = "shortcut"
			services[service].AddCommand(c)
		}
	}
}

// callFlags are the flags shared by every command that calls an endpoint.
type callFlags struct {
	paramsJSON string
	pairs      []string
	pageAll    bool
	pageSize   int
	pageLimit  int
	pageDelay  time.Duration
}

func (f *callFlags) register(fs *pflag.FlagSet, paged bool) {
	fs.StringVar(&f.paramsJSON, "params", "", "request parameters as a JSON object (@file or - for stdin)")
	fs.StringArrayVarP(&f.pairs, "param", "p", nil, "one parameter as key=value, repeatable (value @file reads a file); e.g. -p goods_id-like=A%")
	if paged {
		fs.BoolVar(&f.pageAll, "page-all", false, "fetch every page by advancing offset")
		fs.IntVar(&f.pageSize, "page-size", 1000, "rows per request with --page-all")
		fs.IntVar(&f.pageLimit, "page-limit", 100, "maximum number of requests with --page-all (0 = no limit)")
		fs.DurationVar(&f.pageDelay, "page-delay", 0, "pause between requests with --page-all")
	}
}

// reservedFlags are the global flag names an API parameter must not shadow;
// such a parameter is still reachable through -p / --params.
var reservedFlags = map[string]bool{"format": true, "profile": true, "dry-run": true, "yes": true,
	"wait": true, "no-validate": true, "timeout": true, "help": true, "version": true}

func flagName(param string) string { return strings.ReplaceAll(param, "_", "-") }

func newMethodCmd(ep *registry.Endpoint) *cobra.Command {
	var cf callFlags
	typed := map[string]*string{}
	paged := ep.HasParam("offset") && ep.HasParam("limit")

	long := ep.Summary + "\n\nPOST " + ep.Path + "   [" + ep.Risk + "]\nDocs: " + ep.DocURL +
		"\nSchema: ne-cli schema " + ep.ID
	if ep.Filterable {
		long += "\n\nSearch conditions are parameters named <field>-<operator> (all ANDed), e.g.\n" +
			"  -p " + exampleCondition(ep) + "\n" +
			"Operators: eq neq lt lte gt gte in nin like nlike nlikeornull blank nblank null nnull"
	}
	c := &cobra.Command{
		Use:   ep.Method,
		Short: ep.Summary,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]string{}
			if err := mergeParamsJSON(params, cf.paramsJSON, cmd.InOrStdin()); err != nil {
				return err
			}
			for name, v := range typed {
				if cmd.Flags().Changed(flagName(name)) {
					val, err := readValue(*v, cmd.InOrStdin())
					if err != nil {
						return err
					}
					params[name] = val
				}
			}
			if err := mergeKV(params, cf.pairs, cmd.InOrStdin()); err != nil {
				return err
			}
			return runEndpoint(cmd, ep, params, &cf)
		},
	}
	cf.register(c.Flags(), paged)
	for _, p := range ep.Params {
		name := flagName(p.Name)
		if c.Flags().Lookup(name) != nil || reservedFlags[name] {
			continue
		}
		desc := p.Desc
		if r := []rune(desc); len(r) > 110 {
			desc = string(r[:110]) + "…"
		}
		if p.Required {
			desc = "(required) " + desc
		}
		if p.Name == "fields" {
			desc += ` ("all" = every field)`
		}
		typed[p.Name] = c.Flags().String(name, "", desc)
	}
	return c
}

func exampleCondition(ep *registry.Endpoint) string {
	if len(ep.Fields) == 0 {
		return "<field>-eq=<value>"
	}
	return ep.Fields[0].Name + "-eq=<value>"
}

// runEndpoint validates, confirms, sends (with paging) and prints.
func runEndpoint(cmd *cobra.Command, ep *registry.Endpoint, params map[string]string, cf *callFlags) error {
	if ep.HasParam("fields") && strings.EqualFold(strings.TrimSpace(params["fields"]), "all") {
		names := make([]string, len(ep.Fields))
		for i, f := range ep.Fields {
			names[i] = f.Name
		}
		params["fields"] = strings.Join(names, ",")
	}
	if err := confirmRisk(ep); err != nil {
		return err
	}
	var body map[string]any
	var err error
	if cf != nil && cf.pageAll && !opts.dryRun {
		body, err = sendPaged(cmd.Context(), ep, params, cf)
	} else {
		body, err = send(cmd.Context(), requestFor(ep, params))
	}
	if err != nil {
		return err
	}
	return emit(cmd, body, splitFields(params["fields"]))
}

func confirmRisk(ep *registry.Endpoint) error {
	if ep.Risk != "high-risk-write" || opts.yes || opts.dryRun {
		return nil
	}
	return &cliError{Type: "confirmation_required", exit: exitUsage,
		Message: ep.ID + " (" + ep.Summary + ") is a high-risk write that is hard or impossible to undo",
		Hint:    "preview with --dry-run, confirm with the user, then re-run with --yes"}
}

func splitFields(s string) []string {
	var out []string
	for f := range strings.SplitSeq(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// sendPaged walks offset/limit until a short page, the caller's own limit, or
// --page-limit is reached, and returns one merged body.
func sendPaged(ctx context.Context, ep *registry.Endpoint, params map[string]string, cf *callFlags) (map[string]any, error) {
	if cf.pageSize <= 0 {
		return nil, usageErr("--page-size must be positive")
	}
	offset, _ := strconv.Atoi(params["offset"])
	want := -1 // total rows requested through limit; -1 = everything
	if params["limit"] != "" {
		n, err := strconv.Atoi(params["limit"])
		if err != nil {
			return nil, usageErr("limit must be a number")
		}
		want = n
	}
	var all []any
	var last map[string]any
	hasMore := false
	for page := 1; ; page++ {
		size := cf.pageSize
		if want >= 0 && want-len(all) < size {
			size = want - len(all)
		}
		if size <= 0 {
			break
		}
		p := map[string]string{}
		maps.Copy(p, params)
		p["offset"], p["limit"] = strconv.Itoa(offset), strconv.Itoa(size)
		body, err := send(ctx, requestFor(ep, p))
		if err != nil {
			return nil, err
		}
		last = body
		rows, _ := body["data"].([]any)
		all = append(all, rows...)
		offset += len(rows)
		if len(rows) < size {
			break
		}
		if cf.pageLimit > 0 && page >= cf.pageLimit {
			hasMore = want < 0 || len(all) < want
			break
		}
		if cf.pageDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(cf.pageDelay):
			}
		}
	}
	if last == nil {
		last = map[string]any{"result": "success"}
	}
	if all == nil {
		all = []any{}
	}
	last["data"] = all
	last["count"] = json.Number(strconv.Itoa(len(all)))
	last["has_more"] = hasMore
	if hasMore {
		last["next_offset"] = offset
	}
	return last, nil
}
