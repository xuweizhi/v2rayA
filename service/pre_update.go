package main

import (
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray/asset/dat"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"github.com/v2rayA/v2rayA/server/service"
)

func updateSubscriptions() {
	subs := configure.GetSubscriptions()
	control := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, sub := range subs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			control <- struct{}{}
			defer func() { <-control }()
			if err := service.UpdateScheduledSubscription(i, sub.DatabaseID); err != nil {
				log.Warn("[AutoUpdate] subscription #%d: %v", sub.DatabaseID, err)
			}
		}()
	}
	wg.Wait()
}

func initUpdatingTicker() {
	conf.TickerUpdateGFWList = time.NewTicker(24 * time.Hour * 365 * 100)
	conf.TickerUpdateSubscription = time.NewTicker(24 * time.Hour * 365 * 100)
	go func() {
		for range conf.TickerUpdateGFWList.C {
			_, err := dat.CheckAndUpdateGFWList("")
			if err != nil {
				log.Info("[AutoUpdate] GFWList: %v", err)
			}
		}
	}()
	go func() {
		for range conf.TickerUpdateSubscription.C {
			updateSubscriptions()
		}
	}()
}

func checkUpdate() {
	setting := service.GetSetting()

	// initialize ticker
	initUpdatingTicker()

	// check for PAC file updates
	if setting.GFWListAutoUpdateMode == configure.AutoUpdate ||
		setting.GFWListAutoUpdateMode == configure.AutoUpdateAtIntervals ||
		setting.Transparent == configure.TransparentGfwlist {
		if setting.GFWListAutoUpdateMode == configure.AutoUpdateAtIntervals {
			conf.TickerUpdateGFWList.Reset(configure.AutoUpdateInterval(setting.GFWListAutoUpdateIntervalHour))
		}
		switch setting.RulePortMode {
		case configure.GfwlistMode:
			go func() {
				/* Update LoyalsoldierSite.dat */
				localGFWListVersion, err := dat.CheckAndUpdateGFWList("")
				if err != nil {
					log.Warn("Failed to update PAC file: %v", err.Error())
					return
				}
				log.Info("Complete updating PAC file. Localtime: %v", localGFWListVersion)
			}()
		case configure.CustomMode:
			// obsolete
		}
	}

	// check for subscription updates
	if setting.SubscriptionAutoUpdateMode == configure.AutoUpdate ||
		setting.SubscriptionAutoUpdateMode == configure.AutoUpdateAtIntervals {

		if setting.SubscriptionAutoUpdateMode == configure.AutoUpdateAtIntervals {
			conf.TickerUpdateSubscription.Reset(configure.AutoUpdateInterval(setting.SubscriptionAutoUpdateIntervalHour))
		}
		go updateSubscriptions()
	}
	// check for server updates
	go func() {
		f := func() {
			if foundNew, remote, err := service.CheckUpdate(); err == nil {
				conf.FoundNew = foundNew
				conf.RemoteVersion = remote
			}
		}
		f()
		c := time.Tick(7 * 24 * time.Hour)
		for range c {
			f()
		}
	}()

	// watch network changes to re-probe and re-select the best node
	watchNetworkChanges()

	// poll the notice feed periodically
	service.StartNoticeRefresher()
}
