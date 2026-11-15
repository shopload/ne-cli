package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shopload/ne-cli/internal/registry"
)

func newAPICmd() *cobra.Command {
	var cf callFlags
	var base, noAuth bool
	c := &cobra.Command{
		Use:     "api [POST] <path>",
		Short:   "Call any Next Engine API path directly",
		GroupID: "core",
		Long: `Call any Next Engine API path. Every endpoint is a form-encoded POST.

Catalogued paths get the same validation and safety checks as the API
commands. Unknown paths are sent as given, with tokens attached unless
--no-auth is set.`,
		Example: `  ne-cli api /api_v1_master_goods/search -p fields=goods_id,goods_name -p goods_id-like=A% -p limit=10
  ne-cli api /api_v1_receiveorder_base/count --params '{"receive_order_order_status_id-in":["20","40"]}'
  ne-cli api /api_v1_system_orderstatus/info --format table`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[len(args)-1]
			if len(args) == 2 && !strings.EqualFold(args[0], "POST") {
				return usageErr("the Next Engine API only accepts POST, got %q", args[0])
			}
			params := map[string]string{}
			if err := mergeParamsJSON(params, cf.paramsJSON, cmd.InOrStdin()); err != nil {
				return err
			}
			if err := mergeKV(params, cf.pairs, cmd.InOrStdin()); err != nil {
				return err
			}
			if ep := registry.Find(path); ep != nil {
				return runEndpoint(cmd, ep, params, &cf)
			}
			if cf.pageAll {
				return usageErr("--page-all needs a catalogued search endpoint; %s is not in the catalog", path)
			}
			body, err := send(cmd.Context(), &request{Path: "/" + strings.Trim(path, "/"), Base: base, Auth: !noAuth, Params: params})
			if err != nil {
				return err
			}
			return emit(cmd, body, splitFields(params["fields"]))
		},
	}
	cf.register(c.Flags(), true)
	c.Flags().BoolVar(&base, "base", false, "send to the auth host (base.next-engine.org) — only for uncatalogued paths")
	c.Flags().BoolVar(&noAuth, "no-auth", false, "do not attach access_token/refresh_token — only for uncatalogued paths")
	return c
}
