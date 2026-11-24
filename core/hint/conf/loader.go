// Package conf provides a v2raya-core JSON configuration loader.
// It extends xray-core's JSON loader with support for multiObservatory,
// and automatically injects the v2ray-compatible observatory gRPC service.
// It builds native and custom outbound handlers in configuration order.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
package conf

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	obscmd "github.com/v2rayA/v2raya-core/hint/app/observatory/command"
	multiobs "github.com/v2rayA/v2raya-core/hint/app/observatory/multiobservatory"
	hint_anytls "github.com/v2rayA/v2raya-core/hint/proxy/anytls"
	hint_juicity "github.com/v2rayA/v2raya-core/hint/proxy/juicity"
	hint_mieru "github.com/v2rayA/v2raya-core/hint/proxy/mieru"
	hint_shadowtls "github.com/v2rayA/v2raya-core/hint/proxy/shadowtls"
	hint_tuic "github.com/v2rayA/v2raya-core/hint/proxy/tuic"
	xray_commander "github.com/xtls/xray-core/app/commander"
	xray_proxyman "github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/cmdarg"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/serial"
	xray_core "github.com/xtls/xray-core/core"
	xray_conf "github.com/xtls/xray-core/infra/conf"
	xray_cfgdur "github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
	conf_serial "github.com/xtls/xray-core/infra/conf/serial"
	"github.com/xtls/xray-core/main/confloader"
	"google.golang.org/protobuf/proto"
)

// multiObsEntryJSON represents one observer group in the JSON config.
type multiObsEntryJSON struct {
	Tag             string               `json:"tag"`
	ProbeURL        string               `json:"probeURL"`
	ProbeInterval   xray_cfgdur.Duration `json:"probeInterval"`
	SubjectSelector []string             `json:"subjectSelector"`
	Settings        *obsSettingsJSON     `json:"settings"`
	Burst           *burstObsJSON        `json:"burstObservatory"`
}

// pingConfigJSON is the v5 PingConfigObject used by burstObservatory.
type pingConfigJSON struct {
	Destination   string               `json:"destination"`
	Connectivity  string               `json:"connectivity"`
	Interval      xray_cfgdur.Duration `json:"interval"`
	SamplingCount int                  `json:"samplingCount"`
	Timeout       xray_cfgdur.Duration `json:"timeout"`
	HTTPMethod    string               `json:"httpMethod"`
}

// obsSettingsJSON supports legacy observatory fields and v5 pingConfig.
type obsSettingsJSON struct {
	SubjectSelector []string             `json:"subjectSelector"`
	ProbeURL        string               `json:"probeURL"`
	ProbeInterval   xray_cfgdur.Duration `json:"probeInterval"`
	PingConfig      *pingConfigJSON      `json:"pingConfig"`
}

// burstObsJSON matches v5 burstObservatory object shape.
type burstObsJSON struct {
	SubjectSelector []string        `json:"subjectSelector"`
	PingConfig      *pingConfigJSON `json:"pingConfig"`
}

// multiObsJSON is the top-level JSON structure for the multiObservatory field.
type multiObsJSON struct {
	Observers []multiObsEntryJSON `json:"observers"`
}

// extendedJSON captures v2raya-core extension fields alongside standard xray JSON.
type extendedJSON struct {
	MultiObservatory *multiObsJSON `json:"multiObservatory"`
	BurstObservatory *burstObsJSON `json:"burstObservatory"`
}

// buildOutbound handles the protocols supplied by this core alongside Xray's
// native protocols. The first configured handler is the default outbound.
func buildOutbound(c *xray_conf.OutboundDetourConfig) (*xray_core.OutboundHandlerConfig, error) {
	var settings proto.Message
	switch c.Protocol {
	case "anytls":
		settings = new(hint_anytls.ClientConfig)
	case "juicity":
		settings = new(hint_juicity.ClientConfig)
	case "mieru":
		settings = new(hint_mieru.ClientConfig)
	case "shadowtls":
		settings = new(hint_shadowtls.ClientConfig)
	case "tuic":
		settings = new(hint_tuic.ClientConfig)
	default:
		return c.Build()
	}
	if c.Settings != nil {
		if err := json.Unmarshal(*c.Settings, settings); err != nil {
			return nil, errors.New("invalid ", c.Protocol, " settings for tag ", c.Tag).Base(err)
		}
	}
	// Preserve the transport settings used to reach the proxy server.
	// Transparent mode marks the socket so it bypasses local redirection.
	sender := &xray_proxyman.SenderConfig{}
	if c.StreamSetting != nil {
		stream, err := c.StreamSetting.Build()
		if err != nil {
			return nil, err
		}
		sender.StreamSettings = stream
	}
	return &xray_core.OutboundHandlerConfig{
		Tag:            c.Tag,
		SenderSettings: serial.ToTypedMessage(sender),
		ProxySettings:  serial.ToTypedMessage(settings),
	}, nil
}

// injectMultiObservatory appends the multiobservatory.Config TypedMessage to coreConfig.App.
func injectMultiObservatory(coreConfig *xray_core.Config, mo *multiObsJSON) {
	cfg := &multiobs.Config{}
	for _, e := range mo.Observers {
		observer := normalizeObserverConfig(e)
		if observer == nil {
			continue
		}
		cfg.Observers = append(cfg.Observers, observer)
	}
	if len(cfg.Observers) == 0 {
		return
	}
	coreConfig.App = append(coreConfig.App, serial.ToTypedMessage(cfg))
}

// injectBurstObservatoryAsMulti adapts v5 burstObservatory to a single-group MultiObservatory.
// This keeps v2rayA's per-tag API path working: unknown tag requests fall back to aggregated result.
func injectBurstObservatoryAsMulti(coreConfig *xray_core.Config, burst *burstObsJSON) {
	if burst == nil || len(burst.SubjectSelector) == 0 {
		return
	}
	observer := &multiobs.ObserverConfig{
		Tag:             "_burst_global",
		SubjectSelector: burst.SubjectSelector,
	}
	if burst.PingConfig != nil {
		observer.ProbeUrl = burst.PingConfig.Destination
		observer.ProbeInterval = int64(burst.PingConfig.Interval)
	}
	cfg := &multiobs.Config{Observers: []*multiobs.ObserverConfig{observer}}
	coreConfig.App = append(coreConfig.App, serial.ToTypedMessage(cfg))
}

// normalizeObserverConfig converts legacy + v5 observatory JSON into one runtime observer config.
func normalizeObserverConfig(e multiObsEntryJSON) *multiobs.ObserverConfig {
	subjectSelector := e.SubjectSelector
	probeURL := e.ProbeURL
	probeInterval := e.ProbeInterval

	if e.Settings != nil {
		if len(subjectSelector) == 0 {
			subjectSelector = e.Settings.SubjectSelector
		}
		if probeURL == "" {
			probeURL = e.Settings.ProbeURL
		}
		if probeInterval == 0 {
			probeInterval = e.Settings.ProbeInterval
		}
		if e.Settings.PingConfig != nil {
			if probeURL == "" {
				probeURL = e.Settings.PingConfig.Destination
			}
			if probeInterval == 0 {
				probeInterval = e.Settings.PingConfig.Interval
			}
		}
	}

	if e.Burst != nil {
		if len(subjectSelector) == 0 {
			subjectSelector = e.Burst.SubjectSelector
		}
		if e.Burst.PingConfig != nil {
			if probeURL == "" {
				probeURL = e.Burst.PingConfig.Destination
			}
			if probeInterval == 0 {
				probeInterval = e.Burst.PingConfig.Interval
			}
		}
	}

	if len(subjectSelector) == 0 {
		return nil
	}

	return &multiobs.ObserverConfig{
		Tag:             e.Tag,
		ProbeUrl:        probeURL,
		ProbeInterval:   int64(probeInterval),
		SubjectSelector: subjectSelector,
	}
}

// reorderAppsForAPIReadiness ensures the commander (API/gRPC) app starts before
// the proxyman inbound manager. By default, xray puts InboundConfig first in
// config.App, which means the API port becomes connectable before the gRPC server
// is running — causing "did not connect: context deadline exceeded" from v2rayA's
// ObservatoryProducer. Moving the commander before InboundConfig fixes this race.
func reorderAppsForAPIReadiness(coreConfig *xray_core.Config) {
	const commanderType = "xray.app.commander.Config"
	const inboundMgrType = "xray.app.proxyman.InboundConfig"

	commanderIdx := -1
	inboundIdx := -1
	for i, app := range coreConfig.App {
		switch app.Type {
		case commanderType:
			commanderIdx = i
		case inboundMgrType:
			inboundIdx = i
		}
	}
	// Only reorder if commander exists and is after the inbound manager.
	if commanderIdx <= inboundIdx || commanderIdx < 0 || inboundIdx < 0 {
		return
	}
	commander := coreConfig.App[commanderIdx]
	newApp := make([]*serial.TypedMessage, 0, len(coreConfig.App))
	for i, app := range coreConfig.App {
		if i == inboundIdx {
			newApp = append(newApp, commander)
		}
		if i != commanderIdx {
			newApp = append(newApp, app)
		}
	}
	coreConfig.App = newApp
}

// injectObsCommandService adds the v2ray-compatible ObservatoryService to the commander's

// service list. If no commander is found (no api section), this is a no-op.
func injectCompatService(coreConfig *xray_core.Config) {
	const commanderType = "xray.app.commander.Config"
	for i, app := range coreConfig.App {
		if app.Type != commanderType {
			continue
		}
		msg, err := app.GetInstance()
		if err != nil {
			continue
		}
		cmdCfg, ok := msg.(*xray_commander.Config)
		if !ok {
			continue
		}
		cmdCfg.Service = append(cmdCfg.Service, serial.ToTypedMessage(&obscmd.Config{}))
		coreConfig.App[i] = serial.ToTypedMessage(cmdCfg)
		return
	}
}

func decodeConfig(r io.Reader) (*xray_conf.Config, *extendedJSON, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	config, err := conf_serial.DecodeJSONConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	var ext extendedJSON
	_ = json.Unmarshal(raw, &ext)
	return config, &ext, nil
}

func buildExtendedConfig(config xray_conf.Config, ext *extendedJSON) (*xray_core.Config, error) {
	// Build the ordered outbounds ourselves because Xray's JSON builder only
	// recognizes its native protocols. Config.Override has already merged tags.
	outbounds := config.OutboundConfigs
	config.OutboundConfigs = nil
	coreConfig, err := config.Build()
	if err != nil {
		return nil, err
	}
	for i := range outbounds {
		handler, err := buildOutbound(&outbounds[i])
		if err != nil {
			return nil, errors.New("failed to build outbound ", outbounds[i].Tag).Base(err)
		}
		coreConfig.Outbound = append(coreConfig.Outbound, handler)
	}
	if ext != nil && ext.MultiObservatory != nil {
		injectMultiObservatory(coreConfig, ext.MultiObservatory)
	} else if ext != nil && ext.BurstObservatory != nil {
		injectBurstObservatoryAsMulti(coreConfig, ext.BurstObservatory)
	}
	injectCompatService(coreConfig)
	reorderAppsForAPIReadiness(coreConfig)
	return coreConfig, nil
}

func loadConfigFiles(names []string) (*xray_core.Config, error) {
	var config xray_conf.Config
	var ext *extendedJSON
	for i, name := range names {
		errors.LogInfo(context.Background(), "v2raya-core: reading config: ", name)
		r, err := confloader.LoadConfig(name)
		if err != nil {
			return nil, errors.New("failed to read config: ", name).Base(err)
		}
		fileConfig, fileExt, err := decodeConfig(r)
		if err != nil {
			return nil, errors.New("failed to decode config: ", name).Base(err)
		}
		if i == 0 {
			config = *fileConfig
		} else {
			config.Override(fileConfig, name)
		}
		if fileExt.MultiObservatory != nil || fileExt.BurstObservatory != nil {
			ext = fileExt
		}
	}
	return buildExtendedConfig(config, ext)
}

func init() {
	xray_core.ConfigBuilderForFiles = func(files []*xray_core.ConfigSource) (*xray_core.Config, error) {
		names := make([]string, len(files))
		for i, file := range files {
			names[i] = file.Name
		}
		return loadConfigFiles(names)
	}
	common.Must(xray_core.RegisterConfigLoader(&xray_core.ConfigFormat{
		Name:      "JSON",
		Extension: []string{"json"},
		Loader: func(input interface{}) (*xray_core.Config, error) {
			switch v := input.(type) {
			case cmdarg.Arg:
				return loadConfigFiles(v)
			case io.Reader:
				config, ext, err := decodeConfig(v)
				if err != nil {
					return nil, err
				}
				return buildExtendedConfig(*config, ext)
			default:
				return nil, errors.New("unknown config input type")
			}
		},
	}))
}
