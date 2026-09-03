package controller

import (
	"testing"

	"github.com/v2rayA/v2rayA/db/configure"
)

func TestMigrateAndValidateSettingDefaultsMissingWebdavMode(t *testing.T) {
	setting := configure.NewSetting()
	setting.WebdavConnectionMode = ""

	if err := migrateAndValidateSetting(setting); err != nil {
		t.Fatalf("migrateAndValidateSetting() error = %v", err)
	}
	if setting.WebdavConnectionMode != configure.WebdavConnectionFollowSubscription {
		t.Fatalf("WebdavConnectionMode = %q, want %q", setting.WebdavConnectionMode, configure.WebdavConnectionFollowSubscription)
	}
}

func TestMigrateAndValidateSettingRejectsInvalidWebdavMode(t *testing.T) {
	setting := configure.NewSetting()
	setting.WebdavConnectionMode = configure.WebdavConnectionMode("unexpected")

	if err := migrateAndValidateSetting(setting); err == nil {
		t.Fatal("migrateAndValidateSetting() accepted an invalid WebDAV connection mode")
	}
}
