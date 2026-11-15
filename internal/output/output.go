// Package output renders API results as json, pretty, table, ndjson or csv.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

var Formats = []string{"json", "pretty", "table", "ndjson", "csv"}

func ValidFormat(f string) bool {
	for _, v := range Formats {
		if v == f {
			return true
		}
	}
	return false
}

// Write renders v. For table/ndjson/csv the rows are v["data"] when it is a
// list of objects, otherwise v itself is the single row. columns fixes the
// column order; when empty, keys are sorted.
func Write(w io.Writer, format string, v map[string]any, columns []string) error {
	switch format {
	case "", "json":
		return encode(w, v, "")
	case "pretty":
		return encode(w, v, "  ")
	}
	rows := Rows(v)
	if format == "ndjson" {
		for _, r := range rows {
			if err := encode(w, r, ""); err != nil {
				return err
			}
		}
		return nil
	}
	if len(columns) == 0 {
		columns = keys(rows)
	}
	if format == "csv" {
		cw := csv.NewWriter(w)
		_ = cw.Write(columns)
		for _, r := range rows {
			rec := make([]string, len(columns))
			for i, c := range columns {
				rec[i] = cell(r[c])
			}
			_ = cw.Write(rec)
		}
		cw.Flush()
		return cw.Error()
	}
	return table(w, columns, rows)
}

func encode(w io.Writer, v any, indent string) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	return enc.Encode(v)
}

// Rows extracts the record list from a response envelope.
func Rows(v map[string]any) []map[string]any {
	list, ok := v["data"].([]any)
	if !ok {
		row := map[string]any{}
		for k, val := range v {
			if k != "ok" {
				row[k] = val
			}
		}
		return []map[string]any{row}
	}
	rows := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			rows = append(rows, m)
		} else {
			rows = append(rows, map[string]any{"value": item})
		}
	}
	return rows
}

func keys(rows []map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool, float64, int:
		return fmt.Sprint(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// table prints an aligned text table. Control characters are replaced so a
// value from the API cannot drive the terminal.
func table(w io.Writer, columns []string, rows []map[string]any) error {
	const maxWidth = 40
	grid := make([][]string, 0, len(rows)+1)
	grid = append(grid, columns)
	for _, r := range rows {
		line := make([]string, len(columns))
		for i, c := range columns {
			line[i] = truncate(sanitize(cell(r[c])), maxWidth)
		}
		grid = append(grid, line)
	}
	widths := make([]int, len(columns))
	for _, line := range grid {
		for i, c := range line {
			widths[i] = max(widths[i], width(c))
		}
	}
	for n, line := range grid {
		var sb strings.Builder
		for i, c := range line {
			sb.WriteString(c)
			if i < len(line)-1 {
				sb.WriteString(strings.Repeat(" ", widths[i]-width(c)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(sb.String(), " "))
		if n == 0 {
			var sep []string
			for _, wd := range widths {
				sep = append(sep, strings.Repeat("-", wd))
			}
			fmt.Fprintln(w, strings.Join(sep, "  "))
		}
	}
	return nil
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// width approximates terminal columns: East Asian wide runes count as two.
func width(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	if r >= 0x1100 && (r <= 0x115f || (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x20000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

func truncate(s string, limit int) string {
	if width(s) <= limit {
		return s
	}
	n := 0
	for i, r := range s {
		if n+runeWidth(r) > limit-1 {
			return s[:i] + "…"
		}
		n += runeWidth(r)
	}
	return s
}
