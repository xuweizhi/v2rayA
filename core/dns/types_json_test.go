package dns

import (
	"encoding/json"
	"testing"
)

func TestRuleConfigQueryTypeUnmarshalJSON(t *testing.T) {
	var rule RuleConfig
	if err := json.Unmarshal([]byte(`{"query_type":["A","aaaa",15,"TYPE65","16"]}`), &rule); err != nil {
		t.Fatalf("unmarshal rule config: %v", err)
	}

	want := []QueryType{TypeA, TypeAAAA, TypeMX, QueryType(65), TypeTXT}
	if len(rule.QueryType) != len(want) {
		t.Fatalf("query type count = %d, want %d", len(rule.QueryType), len(want))
	}
	for i, qtype := range want {
		if rule.QueryType[i] != qtype {
			t.Errorf("query type[%d] = %d, want %d", i, rule.QueryType[i], qtype)
		}
	}
}

func TestRejectInvalidRuleQueryType(t *testing.T) {
	for _, value := range []string{`"unknown"`, `"1junk"`, `"TYPE65536"`, `65536`, `null`, `-1`} {
		var rule RuleConfig
		if err := json.Unmarshal([]byte(`{"query_type":[`+value+`]}`), &rule); err == nil {
			t.Errorf("accepted invalid query type %s", value)
		}
	}
}
