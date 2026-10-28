package common_test

import (
	"testing"

	"github.com/v2rayA/v2rayA/common"
)

func TestUrlEncoded(t *testing.T) {
	str := `试试1+就试试!`
	if got := common.UrlEncoded(str); got != "%E8%AF%95%E8%AF%951+%E5%B0%B1%E8%AF%95%E8%AF%95%21" {
		t.Fatalf("unexpected encoding: %s", got)
	}
}

func TestFillEmpty(t *testing.T) {
	type settings struct {
		Name    string
		Port    int
		Enabled bool
	}
	defaults := settings{Name: "default", Port: 8080, Enabled: true}
	setting := settings{Name: "custom"}
	if err := common.FillEmpty(&setting, &defaults); err != nil {
		t.Fatal(err)
	}
	// Explicit false values must survive applying defaults.
	if setting != (settings{Name: "custom", Port: 8080, Enabled: false}) {
		t.Fatalf("unexpected filled settings: %+v", setting)
	}
	if err := common.FillEmpty(setting, defaults); err != common.NeedPassInPointer {
		t.Fatalf("non-pointer should be rejected: %v", err)
	}
}
