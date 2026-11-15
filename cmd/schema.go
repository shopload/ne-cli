package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shopload/ne-cli/internal/registry"
)

func newSchemaCmd() *cobra.Command {
	var service, search, grep string
	c := &cobra.Command{
		Use:     "schema [id|path]",
		Short:   "Inspect endpoints: parameters, fields, types and risk (offline)",
		GroupID: "core",
		Long: `Without an argument, list every endpoint. With an endpoint id or path, print
its contract: request parameters, the fields usable in "fields" and in search
conditions (with their types), XML data fields, and the risk level.

The catalog is embedded in the binary, so this never calls the API.`,
		Example: `  ne-cli schema --format table
  ne-cli schema --service receiveorder
  ne-cli schema --search 在庫
  ne-cli schema receiveorder.base.search --grep date
  ne-cli schema /api_v1_master_goods/upload
  ne-cli schema errors 004002`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listEndpoints(cmd, service, search)
			}
			ep := registry.Find(args[0])
			if ep == nil {
				return &cliError{Type: "usage", Message: "unknown endpoint " + args[0], Hint: "run `ne-cli schema` to list endpoint ids", exit: exitUsage}
			}
			out := map[string]any{
				"id": ep.ID, "path": ep.Path, "summary": ep.Summary, "category": ep.Tag,
				"command": "ne-cli " + ep.Service + " " + ep.Resource + " " + ep.Method,
				"risk":    ep.Risk, "doc_url": ep.DocURL, "params": ep.Params,
			}
			if ep.Filterable {
				out["conditions"] = "<field>-<operator>=<value>, ANDed. -eq/-neq: any type; -lt/-lte/-gt/-gte: number, datetime; " +
					"-in/-nin: string, number (max 1000 values); -like/-nlike/-nlikeornull: string; -blank/-nblank/-null/-nnull: any type, no value"
			}
			if len(ep.Fields) > 0 {
				fields := ep.Fields
				if grep != "" {
					fields = nil
					for _, f := range ep.Fields {
						if strings.Contains(f.Name, grep) || strings.Contains(f.Desc, grep) {
							fields = append(fields, f)
						}
					}
				}
				out["fields"] = fields
				// table/csv/ndjson show the field list
				rows := make([]any, len(fields))
				for i, f := range fields {
					rows[i] = map[string]any{"name": f.Name, "type": f.Type, "desc": f.Desc}
				}
				if opts.format != "json" && opts.format != "pretty" {
					return emit(cmd, map[string]any{"data": rows}, []string{"name", "type", "desc"})
				}
			}
			if len(ep.DataFields) > 0 {
				out["data_fields"] = ep.DataFields
			}
			if len(ep.Response) > 0 {
				out["response"] = ep.Response
			}
			return emit(cmd, out, nil)
		},
	}
	c.Flags().StringVar(&service, "service", "", "list only this service (e.g. receiveorder, master)")
	c.Flags().StringVar(&search, "search", "", "list endpoints whose id, path, summary or category contains this text")
	c.Flags().StringVar(&grep, "grep", "", "with an endpoint: only fields whose name or description contains this text")
	c.AddCommand(&cobra.Command{
		Use:   "errors [code]",
		Short: "Look up Next Engine message codes (meaning, retryable or not)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var rows []any
			for _, m := range registry.Load().Messages {
				if len(args) == 0 || m.Code == args[0] {
					rows = append(rows, map[string]any{"code": m.Code, "message": m.Message, "retryable": m.Retry, "note": m.Note})
				}
			}
			if len(rows) == 0 {
				return &cliError{Type: "usage", Message: "unknown message code " + args[0], exit: exitUsage}
			}
			return emit(cmd, map[string]any{"data": rows}, []string{"code", "retryable", "message", "note"})
		},
	})
	return c
}

func listEndpoints(cmd *cobra.Command, service, search string) error {
	rows := []any{}
	for _, ep := range registry.Load().Endpoints {
		if service != "" && ep.Service != service {
			continue
		}
		if search != "" && !strings.Contains(ep.ID+" "+ep.Path+" "+ep.Summary+" "+ep.Tag, search) {
			continue
		}
		rows = append(rows, map[string]any{"id": ep.ID, "path": ep.Path, "risk": ep.Risk, "summary": ep.Summary, "category": ep.Tag})
	}
	return emit(cmd, map[string]any{"count": len(rows), "data": rows}, []string{"id", "risk", "summary", "path"})
}
