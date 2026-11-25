package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/gin-gonic/gin"
	jsonIteratorExtra "github.com/json-iterator/go/extra"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/kernel/v2ray/asset"
	"github.com/v2rayA/v2rayA/kernel/v2ray/asset/dat"
	"github.com/v2rayA/v2rayA/kernel/v2ray/where"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"github.com/v2rayA/v2rayA/server/service"
)

func initializeEmptyDatabase() error {
	empty, err := db.IsEmpty()
	if err != nil || !empty {
		return err
	}

	var links []string
	if runtime.GOOS != "windows" {
		links, err = readV4ImportLinks(conf.V4ConfigDir())
		if err != nil {
			return fmt.Errorf("read v4 database: %w", err)
		}
	}
	defaults := configure.New()
	initialized, err := db.InitDefaultsIfEmpty(defaults.Setting, defaults.Ports)
	if err != nil || !initialized {
		return err
	}
	for _, link := range links {
		if err := service.Import(link, nil, ""); err != nil {
			log.Warn("Failed to migrate v4 entry: %v", err)
		}
	}
	return nil
}

// openMigratedDatabase is shared by startup and offline account reset.
func openMigratedDatabase() error {
	err := db.Open()
	if errors.Is(err, db.ErrNeedMigration) {
		if err := db.MigrateFromBoltDB(); err != nil {
			return err
		}
		return db.Open()
	}
	return err
}

func initConfigure() {
	// initialize configuration
	jsonIteratorExtra.RegisterFuzzyDecoders()

	// Apply a pending database restore before the DB is opened.
	if err := service.ApplyPendingRestore(); err != nil {
		log.Error("failed to apply pending database restore: %v", err)
	}

	if err := openMigratedDatabase(); err != nil {
		log.Fatal("Failed to initialize database: %v", err)
	}
	if err := initializeEmptyDatabase(); err != nil {
		log.Fatal("First-run configuration: %v", err)
	}
	if err := configure.InitDefaultOutbound(); err != nil {
		log.Warn("initDefaultOutbound: %v", err)
	}

	if groups := configure.GetTproxyWhiteIpGroups(); len(groups.CountryCodes) == 0 && len(groups.CustomIps) == 0 {
		_ = configure.SetTproxyWhiteIpGroups([]string{"PRIVATE"}, []string{})
	}

	// check if config.json exists
	if _, err := os.Stat(asset.GetV2rayConfigPath()); err != nil {
		// if not exists, create one. This mostly happens when mounting a volume in docker mode and it covers /etc/v2ray.
		t := v2ray.Template{}
		_ = v2ray.WriteV2rayConfig(t.ToConfigBytes())
	}

	// first determine if v2ray exists
	if _, err := where.GetV2rayBinPath(); err == nil {
		// check if geoip, geosite exist
		if !asset.DoesV2rayAssetExist("geoip.dat") || !asset.DoesV2rayAssetExist("geosite.dat") {
			log.Alert("downloading missing geoip.dat and geosite.dat")
			var l net.Listener
			if l, err = net.Listen("tcp", conf.GetEnvironmentConfig().Address); err != nil {
				log.Fatal("net.Listen: %v", err)
			}
			e := gin.New()
			e.GET("/", func(c *gin.Context) {
				c.Header("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
				c.Header("Pragma", "no-cache")
				c.Header("Expires", "0")
				c.String(200, "Downloading missing geoip.dat and geosite.dat; refresh the page later.")
			})
			go e.RunListener(l)
			if !asset.DoesV2rayAssetExist("geoip.dat") {
				err := dat.UpdateLocalGeoIP()
				if err != nil {
					log.Fatal("UpdateLocalGeoIP: %v", err)
				}
			}
			if !asset.DoesV2rayAssetExist("geosite.dat") {
				err = dat.UpdateLocalGeoSite()
				if err != nil {
					log.Fatal("UpdateLocalGeoSite: %v", err)
				}
			}
			if l != nil {
				l.Close()
			}
			log.Alert("geoip.dat and geosite.dat are ready")
		}
	}
}
