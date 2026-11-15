package cmd

import (
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shopload/ne-cli/internal/registry"
)

// shortcuts are task-level helpers, shown as `ne-cli <service> +<name>`.
var shortcuts = map[string]func() []*cobra.Command{
	"receiveorder": func() []*cobra.Command { return []*cobra.Command{newOrderListCmd(), newOrderUpdateCmd()} },
	"master":       func() []*cobra.Command { return []*cobra.Command{newStockCmd()} },
	"system":       func() []*cobra.Command { return []*cobra.Command{newQueWaitCmd()} },
}

func call(ctx context.Context, id string, params map[string]string) (map[string]any, error) {
	return send(ctx, requestFor(registry.Find(id), params))
}

var dateOnly = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// dayBound turns a bare date into the first or last second of that day.
func dayBound(v string, end bool) string {
	if !dateOnly.MatchString(v) {
		return v
	}
	if end {
		return v + " 23:59:59"
	}
	return v + " 00:00:00"
}

func newOrderListCmd() *cobra.Command {
	var status, from, to, shopIDs, fields, rowFields string
	var includeCancelled, withRows bool
	var limit int
	var pairs []string
	c := &cobra.Command{
		Use:   "+list",
		Short: "List orders with sensible defaults (cancelled excluded, optional line items)",
		Long: `Searches order headers (受注伝票) and, with --with-rows, attaches each order's
line items (受注明細) under "rows".

It applies the rules that are easy to get wrong by hand:
  - cancelled orders are excluded with receive_order_cancel_type_id-eq=0
    (order status alone does not tell you an order was cancelled)
  - status codes are strings, so they are matched with -in, never a range
  - line items are fetched in a second search keyed by order id, because
    header conditions do not apply to the row search`,
		Example: `  ne-cli receiveorder +list --status 20 --format table
  ne-cli receiveorder +list --from 2026-11-01 --to 2026-11-15 --shop-ids 3,7 --with-rows
  ne-cli receiveorder +list -p 'receive_order_gruoping_tag-like=%[急ぎ]%'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]string{"fields": fields, "limit": strconv.Itoa(limit)}
			if status != "" {
				params["receive_order_order_status_id-in"] = status
			}
			if from != "" {
				params["receive_order_date-gte"] = dayBound(from, false)
			}
			if to != "" {
				params["receive_order_date-lte"] = dayBound(to, true)
			}
			if shopIDs != "" {
				params["receive_order_shop_id-in"] = shopIDs
			}
			if !includeCancelled {
				params["receive_order_cancel_type_id-eq"] = "0"
			}
			if err := mergeKV(params, pairs, cmd.InOrStdin()); err != nil {
				return err
			}
			cols := splitFields(params["fields"])
			if withRows && !contains(cols, "receive_order_id") {
				params["fields"] = "receive_order_id," + params["fields"]
			}
			body, err := call(cmd.Context(), "receiveorder.base.search", params)
			if err != nil || opts.dryRun || !withRows {
				if err != nil {
					return err
				}
				return emit(cmd, body, cols)
			}
			orders, _ := body["data"].([]any)
			if err := attachRows(cmd.Context(), orders, rowFields); err != nil {
				return err
			}
			return emit(cmd, body, append(cols, "rows"))
		},
	}
	f := c.Flags()
	f.StringVar(&status, "status", "", "order status codes, comma-separated (0,1,2,20,30,40,50; see `ne-cli system orderstatus info`)")
	f.StringVar(&from, "from", "", "order date from (YYYY-MM-DD or YYYY-MM-DD hh:mm:ss)")
	f.StringVar(&to, "to", "", "order date to (a bare date includes the whole day)")
	f.StringVar(&shopIDs, "shop-ids", "", "shop ids, comma-separated (see `ne-cli master shop search`)")
	f.BoolVar(&includeCancelled, "include-cancelled", false, "also return cancelled orders")
	f.BoolVar(&withRows, "with-rows", false, "attach line items to each order as \"rows\"")
	f.IntVar(&limit, "limit", 100, "maximum number of orders")
	f.StringVar(&fields, "fields", "receive_order_id,receive_order_shop_id,receive_order_shop_cut_form_id,receive_order_date,"+
		"receive_order_order_status_id,receive_order_order_status_name,receive_order_cancel_type_id,"+
		"receive_order_total_amount,receive_order_last_modified_date", "header fields")
	f.StringVar(&rowFields, "row-fields", "receive_order_row_receive_order_id,receive_order_row_no,receive_order_row_goods_id,"+
		"receive_order_row_goods_name,receive_order_row_quantity,receive_order_row_unit_price,"+
		"receive_order_row_sub_total_price,receive_order_row_cancel_flag", "line item fields with --with-rows")
	f.StringArrayVarP(&pairs, "param", "p", nil, "extra header condition as key=value, repeatable")
	return c
}

// attachRows fetches the line items of orders in chunks and groups them.
func attachRows(ctx context.Context, orders []any, rowFields string) error {
	const key = "receive_order_row_receive_order_id"
	if !contains(splitFields(rowFields), key) {
		rowFields = key + "," + rowFields
	}
	byOrder := map[string][]any{}
	var ids []string
	for _, o := range orders {
		if m, ok := o.(map[string]any); ok {
			ids = append(ids, fmt.Sprint(m["receive_order_id"]))
		}
	}
	ep := registry.Find("receiveorder.row.search")
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		body, err := sendPaged(ctx, ep, map[string]string{"fields": rowFields, key + "-in": strings.Join(chunk, ",")},
			&callFlags{pageSize: 5000})
		if err != nil {
			return err
		}
		rows, _ := body["data"].([]any)
		for _, r := range rows {
			if m, ok := r.(map[string]any); ok {
				id := fmt.Sprint(m[key])
				byOrder[id] = append(byOrder[id], m)
			}
		}
	}
	for _, o := range orders {
		if m, ok := o.(map[string]any); ok {
			rows := byOrder[fmt.Sprint(m["receive_order_id"])]
			if rows == nil {
				rows = []any{}
			}
			m["rows"] = rows
		}
	}
	return nil
}

// xmlSections maps the documented section of an updatable field to its element.
var xmlSections = map[string]string{"受注伝票": "receiveorder_base", "受注伝票オプション": "receiveorder_option", "受注明細": "receiveorder_row"}

func newOrderUpdateCmd() *cobra.Command {
	var id, lastModified string
	var sets, rowSets []string
	var shippedUpdate, rowCancelUpdate, recalculate bool
	c := &cobra.Command{
		Use:   "+update",
		Short: "Update one order from key=value pairs (builds the XML and the optimistic lock for you)",
		Long: `Updates a single order (受注伝票更新) without hand-writing XML.

--set takes header and option fields; --row-set takes line item fields prefixed
with the row number. The current receive_order_last_modified_date, which the
API requires as an optimistic lock, is looked up unless --last-modified is given.

Things to know before writing:
  - free-text fields (receive_order_note, receive_order_worker_text, …) are
    replaced, not appended to: read the current value first if it must be kept
  - receive_order_confirm_check_id is written as 1 = check, 0 = uncheck, which
    differs from the values a search returns
  - this cannot ship an order; use ` + "`ne-cli receiveorder base shipped`" + `

List the writable fields with: ne-cli schema receiveorder.base.update`,
		Example: `  ne-cli receiveorder +update --id 123 --set receive_order_gruoping_tag='[処理済]' --dry-run
  ne-cli receiveorder +update --id 123 --row-set 1:receive_order_row_cancel_flag=1 --recalculate`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" || len(sets)+len(rowSets) == 0 {
				return usageErr("--id and at least one --set or --row-set are required")
			}
			ep := registry.Find("receiveorder.base.update")
			data, err := buildOrderXML(ep, sets, rowSets)
			if err != nil {
				return err
			}
			if lastModified == "" {
				if opts.dryRun {
					lastModified = "<looked up at run time>"
				} else {
					body, err := call(cmd.Context(), "receiveorder.base.search", map[string]string{
						"fields": "receive_order_id,receive_order_last_modified_date", "receive_order_id-eq": id})
					if err != nil {
						return err
					}
					rows, _ := body["data"].([]any)
					if len(rows) != 1 {
						return &cliError{Type: "not_found", Message: "no order with receive_order_id " + id, exit: exitAPI}
					}
					lastModified, _ = rows[0].(map[string]any)["receive_order_last_modified_date"].(string)
				}
			}
			params := map[string]string{"receive_order_id": id, "receive_order_last_modified_date": lastModified, "data": data}
			if shippedUpdate {
				params["receive_order_shipped_update_flag"] = "1"
			}
			if rowCancelUpdate {
				params["receive_order_row_cancel_update_flag"] = "1"
			}
			if recalculate {
				params["receive_order_recalculate_flag"] = "1"
			}
			body, err := call(cmd.Context(), ep.ID, params)
			if err != nil {
				return err
			}
			body["receive_order_id"] = id
			return emit(cmd, body, nil)
		},
	}
	f := c.Flags()
	f.StringVar(&id, "id", "", "receive_order_id (伝票番号) of the order to update")
	f.StringArrayVar(&sets, "set", nil, "header/option field as field=value, repeatable")
	f.StringArrayVar(&rowSets, "row-set", nil, "line item field as <row_no>:field=value, repeatable")
	f.StringVar(&lastModified, "last-modified", "", "receive_order_last_modified_date from a previous search (looked up when omitted)")
	f.BoolVar(&shippedUpdate, "allow-shipped", false, "allow updating an order that is already shipped (receive_order_shipped_update_flag=1)")
	f.BoolVar(&rowCancelUpdate, "row-cancel-update", false, "send receive_order_row_cancel_update_flag=1")
	f.BoolVar(&recalculate, "recalculate", false, "recalculate header amounts (receive_order_recalculate_flag=1)")
	return c
}

type xmlElem struct {
	XMLName xml.Name
	Value   string    `xml:"value,attr,omitempty"`
	Text    string    `xml:",chardata"`
	Kids    []xmlElem `xml:",any"`
}

// buildOrderXML renders the `data` document of the order update API.
func buildOrderXML(ep *registry.Endpoint, sets, rowSets []string) (string, error) {
	section := func(name string) (string, error) {
		for _, f := range ep.DataFields {
			if f.Name == name {
				return xmlSections[f.Section], nil
			}
		}
		return "", &cliError{Type: "validation", Message: "field " + name + " cannot be updated through this API",
			Hint: "run `ne-cli schema receiveorder.base.update` and look at data_fields", exit: exitUsage}
	}
	groups := map[string]*xmlElem{}
	group := func(name string) *xmlElem {
		if groups[name] == nil {
			groups[name] = &xmlElem{XMLName: xml.Name{Local: name}}
		}
		return groups[name]
	}
	for _, s := range sets {
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return "", usageErr("--set expects field=value, got %q", s)
		}
		sec, err := section(k)
		if err != nil {
			return "", err
		}
		if sec == "receiveorder_row" {
			return "", usageErr("%s is a line item field; use --row-set <row_no>:%s=…", k, k)
		}
		g := group(sec)
		g.Kids = append(g.Kids, xmlElem{XMLName: xml.Name{Local: k}, Text: v})
	}
	rowIndex := map[string]int{}
	for _, s := range rowSets {
		no, rest, ok := strings.Cut(s, ":")
		k, v, ok2 := strings.Cut(rest, "=")
		if _, err := strconv.Atoi(no); !ok || !ok2 || err != nil {
			return "", usageErr("--row-set expects <row_no>:field=value, got %q", s)
		}
		if sec, err := section(k); err != nil {
			return "", err
		} else if sec != "receiveorder_row" {
			return "", usageErr("%s is not a line item field; use --set", k)
		}
		g := group("receiveorder_row")
		i, seen := rowIndex[no]
		if !seen {
			g.Kids = append(g.Kids, xmlElem{XMLName: xml.Name{Local: "receive_order_row_no"}, Value: no})
			i = len(g.Kids) - 1
			rowIndex[no] = i
		}
		g.Kids[i].Kids = append(g.Kids[i].Kids, xmlElem{XMLName: xml.Name{Local: k}, Text: v})
	}
	root := xmlElem{XMLName: xml.Name{Local: "root"}}
	for _, name := range []string{"receiveorder_base", "receiveorder_option", "receiveorder_row"} {
		if g := groups[name]; g != nil {
			root.Kids = append(root.Kids, *g)
		}
	}
	b, err := xml.Marshal(root)
	if err != nil {
		return "", err
	}
	return `<?xml version="1.0" encoding="utf-8"?>` + string(b), nil
}

func newStockCmd() *cobra.Command {
	var goodsIDs, like, fields string
	var limit int
	c := &cobra.Command{
		Use:   "+stock",
		Short: "Look up stock quantities by product code",
		Example: `  ne-cli master +stock --goods-ids A001,A002 --format table
  ne-cli master +stock --like 'A0%' --limit 500`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]string{"fields": fields, "limit": strconv.Itoa(limit)}
			if goodsIDs != "" {
				params["stock_goods_id-in"] = goodsIDs
			}
			if like != "" {
				params["stock_goods_id-like"] = like
			}
			body, err := call(cmd.Context(), "master.stock.search", params)
			if err != nil {
				return err
			}
			return emit(cmd, body, splitFields(fields))
		},
	}
	f := c.Flags()
	f.StringVar(&goodsIDs, "goods-ids", "", "product codes, comma-separated (max 1000)")
	f.StringVar(&like, "like", "", "product code pattern with % wildcards")
	f.IntVar(&limit, "limit", 100, "maximum number of rows")
	f.StringVar(&fields, "fields", "stock_goods_id,stock_quantity,stock_allocation_quantity,stock_free_quantity,"+
		"stock_defective_quantity,stock_remaining_order_quantity,stock_out_quantity", "stock fields")
	return c
}

func newQueWaitCmd() *cobra.Command {
	var queID string
	var interval, maxWait time.Duration
	c := &cobra.Command{
		Use:   "+que-wait",
		Short: "Wait for an upload queue entry (que_id) to finish",
		Long: `Upload APIs (goods, set goods, warehouse stock, orders) only enqueue the file
and return a que_id. This polls the upload queue until the entry succeeds
(que_status_id 2) or fails (-1, exit code 1 with the queue message).`,
		Example: `  ne-cli master goods upload --data-type csv --data @goods.csv
  ne-cli system +que-wait --que-id 189`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if queID == "" {
				return usageErr("--que-id is required")
			}
			params := map[string]string{"fields": "que_id,que_method_name,que_status_id,que_message,que_last_modified_date", "que_id-eq": queID}
			deadline := time.Now().Add(maxWait)
			for {
				body, err := call(cmd.Context(), "system.que.search", params)
				if err != nil || opts.dryRun {
					if err != nil {
						return err
					}
					return emit(cmd, body, nil)
				}
				rows, _ := body["data"].([]any)
				if len(rows) == 0 {
					return &cliError{Type: "not_found", Message: "no upload queue entry with que_id " + queID, exit: exitAPI}
				}
				row, _ := rows[0].(map[string]any)
				switch fmt.Sprint(row["que_status_id"]) {
				case "2":
					return emit(cmd, map[string]any{"status": "succeeded", "data": rows}, nil)
				case "-1":
					return &cliError{Type: "que_failed", Message: row["que_message"], Details: row, exit: exitAPI}
				}
				if time.Now().After(deadline) {
					return &cliError{Type: "timeout", Message: "the upload is still being processed", Details: row, Retry: "yes",
						Hint: "run the same command again later", exit: exitAPI}
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(interval):
				}
			}
		},
	}
	f := c.Flags()
	f.StringVar(&queID, "que-id", "", "que_id returned by an upload API")
	f.DurationVar(&interval, "interval", 5*time.Second, "polling interval")
	f.DurationVar(&maxWait, "max-wait", 10*time.Minute, "give up after this long")
	return c
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
