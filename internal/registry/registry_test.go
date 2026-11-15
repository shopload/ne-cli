package registry

import (
	"regexp"
	"testing"
)

func TestCatalogIsConsistent(t *testing.T) {
	m := Load()
	if len(m.Endpoints) != 131 {
		t.Fatalf("got %d endpoints, want 131", len(m.Endpoints))
	}
	name := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	seen := map[string]bool{}
	for _, e := range m.Endpoints {
		if seen[e.ID] || !name.MatchString(e.Service) || !name.MatchString(e.Resource) || !name.MatchString(e.Method) {
			t.Errorf("bad or duplicate id %q", e.ID)
		}
		seen[e.ID] = true
		if Find(e.Path) != e || Find(e.ID) != e {
			t.Errorf("%s is not findable by id and path", e.ID)
		}
		if e.Filterable && e.Method == "search" && (len(e.Fields) == 0 || !e.HasParam("fields")) {
			t.Errorf("%s: search endpoint without fields metadata", e.ID)
		}
		if e.Risk == "read" && (e.Method == "update" || e.Method == "upload" || e.Method == "shipped") {
			t.Errorf("%s is classified as read", e.ID)
		}
	}
	if FindMessage("002004") == nil {
		t.Error("message codes are missing")
	}
}

func TestValidate(t *testing.T) {
	e := Find("receiveorder.base.search")
	ok := map[string]string{"fields": "receive_order_id, receive_order_date", "receive_order_order_status_id-in": "20,40",
		"receive_order_date-gte": "2026-01-01 00:00:00", "receive_order_gruoping_tag-nlikeornull": "%x%", "limit": "10"}
	if issues := e.Validate(ok); len(issues) != 0 {
		t.Errorf("valid request rejected: %+v", issues)
	}
	bad := map[string]string{"fields": "receive_order_id", "receive_order_order_status_id-gt": "20"}
	if issues := e.Validate(bad); len(issues) != 1 || issues[0].Hint == "" {
		t.Errorf("range on a string code not caught: %+v", issues)
	}
	if f, op, ok := SplitCondition("a_b-nlikeornull"); !ok || f != "a_b" || op != "nlikeornull" {
		t.Errorf("SplitCondition: %s %s %v", f, op, ok)
	}
}
