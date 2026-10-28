package db

import (
	"testing"

	"github.com/v2rayA/v2rayA/conf"
)

func TestSubscriptionUpdateAtomicAndPreservesReferences(t *testing.T) {
	conf.GetEnvironmentConfig().Config = t.TempDir()
	t.Cleanup(func() { _ = Close() })
	database := GetDB()
	for _, q := range []string{
		"INSERT INTO subscriptions (id, address, remarks, sort) VALUES (1,'https://example.test','old',0)",
		"INSERT INTO servers (id,type,sub_id,config_json,sort) VALUES (1,'subscription_server',1,'{\"name\":\"old\"}',0)",
		"INSERT INTO outbound_names (name) VALUES ('proxy')",
		"INSERT INTO outbound_connections (outbound_name,server_id) VALUES ('proxy',1)",
		"CREATE TRIGGER reject_test_node BEFORE INSERT ON servers WHEN NEW.config_json LIKE '%reject%' BEGIN SELECT RAISE(ABORT,'test failure'); END",
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	value := map[string]interface{}{"address": "https://example.test", "remarks": "new", "servers": []interface{}{map[string]string{"name": "changed"}, map[string]string{"name": "reject"}}}
	if err := ListSet("touch", "subscriptions", 0, value); err == nil {
		t.Fatal("expected injected write failure")
	}
	var remark, config string
	var count int
	if err := database.QueryRow("SELECT remarks FROM subscriptions WHERE id=1").Scan(&remark); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT config_json FROM servers WHERE id=1").Scan(&config); err != nil {
		t.Fatal(err)
	}
	if remark != "old" || config != "{\"name\":\"old\"}" {
		t.Fatalf("partial update persisted: %s %s", remark, config)
	}
	value["servers"] = []interface{}{map[string]string{"name": "changed"}}
	if err := ListSet("touch", "subscriptions", 0, value); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM outbound_connections WHERE server_id=1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("reference lost: %d %v", count, err)
	}
}
