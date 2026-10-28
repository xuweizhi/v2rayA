package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/ipforward"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/kernel/v2ray/asset"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

func GetSetting() *configure.Setting {
	s := configure.GetSettingNotNil()
	if s == nil {
		s = configure.NewSetting()
		_ = configure.SetSetting(s)
	}
	if s.LogLevel == "" {
		s.LogLevel = conf.GetEnvironmentConfig().LogLevel
	}
	return s
}

func UpdateSetting(setting *configure.Setting) (err error) {
	if err := setting.Validate(); err != nil {
		return err
	}
	if setting.LogLevel == "" {
		setting.LogLevel = conf.GetEnvironmentConfig().LogLevel
	}
	if !configure.IsValidWebdavConnectionMode(setting.WebdavConnectionMode) {
		return fmt.Errorf("invalid WebDAV connection mode")
	}
	if (setting.Transparent == configure.TransparentGfwlist || setting.RulePortMode == configure.GfwlistMode) && !asset.DoesV2rayAssetExist("LoyalsoldierSite.dat") {
		return fmt.Errorf("cannot find GFWList files. update GFWList and try again")
	}
	previous := GetSetting()
	wasRunning := v2ray.ProcessManager.Running()
	ipForwardWasOn := ipforward.IsIpForwardOn()
	defer func() {
		if err == nil {
			return
		}
		err = errors.Join(err, configure.SetSetting(previous))
		log.SetLogLevel(previous.LogLevel)
		if ipforward.IsIpForwardOn() != ipForwardWasOn {
			err = errors.Join(err, ipforward.WriteIpForward(ipForwardWasOn))
		}
		if wasRunning && !v2ray.ProcessManager.Running() {
			err = errors.Join(err, v2ray.UpdateV2RayConfig())
		}
	}()
	if setting.IpForward != ipforward.IsIpForwardOn() {
		e := ipforward.WriteIpForward(setting.IpForward)
		if e != nil {
			log.Warn("UpdateSetting: %v", e)
		}
	}
	err = configure.SetSetting(setting)
	if err != nil {
		return
	}
	log.SetLogLevel(setting.LogLevel)
	// If v2ray is running and has connections, rewrite config and restart connection to make changes to transparent proxy, TCPFastOpen, etc. take effect immediately.
	css := configure.GetConnectedServers()
	if v2ray.ProcessManager.Running() && css.Len() > 0 {
		err = v2ray.UpdateV2RayConfig()
		if err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
	}
	if conf.TickerUpdateGFWList != nil {
		if setting.GFWListAutoUpdateMode == configure.AutoUpdateAtIntervals {
			conf.TickerUpdateGFWList.Reset(configure.AutoUpdateInterval(setting.GFWListAutoUpdateIntervalHour))
		} else {
			conf.TickerUpdateGFWList.Reset(24 * time.Hour * 365 * 100)
		}
	}
	if conf.TickerUpdateSubscription != nil {
		if setting.SubscriptionAutoUpdateMode == configure.AutoUpdateAtIntervals {
			conf.TickerUpdateSubscription.Reset(configure.AutoUpdateInterval(setting.SubscriptionAutoUpdateIntervalHour))
		} else {
			conf.TickerUpdateSubscription.Reset(24 * time.Hour * 365 * 100)
		}
	}
	return
}
