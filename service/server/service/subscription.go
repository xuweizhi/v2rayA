package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/common/httpClient"
	"github.com/v2rayA/v2rayA/common/resolv"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/kernel/touch"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

type SIP008 struct {
	Version        int    `json:"version"`
	Username       string `json:"username"`
	UserUUID       string `json:"user_uuid"`
	BytesUsed      uint64 `json:"bytes_used"`
	BytesRemaining uint64 `json:"bytes_remaining"`
	Servers        []struct {
		Server     string `json:"server"`
		ServerPort int    `json:"server_port"`
		Password   string `json:"password"`
		Method     string `json:"method"`
		Plugin     string `json:"plugin"`
		PluginOpts string `json:"plugin_opts"`
		Remarks    string `json:"remarks"`
		ID         string `json:"id"`
	} `json:"servers"`
}

func resolveSIP008(raw string) (infos []serverObj.ServerObj, sip SIP008, err error) {
	err = jsoniter.Unmarshal([]byte(raw), &sip)
	if err != nil {
		return
	}
	for _, server := range sip.Servers {
		rawQuery := url.Values{}
		if server.Plugin != "" {
			// SIP008's "plugin" is the plugin name and "plugin_opts" its options;
			// combine them into the ss:// "plugin=name;opts" parameter.
			plugin := server.Plugin
			if server.PluginOpts != "" {
				plugin += ";" + server.PluginOpts
			}
			rawQuery.Set("plugin", plugin)
		}
		u := url.URL{
			Scheme:   "ss",
			User:     url.UserPassword(server.Method, server.Password),
			Host:     net.JoinHostPort(server.Server, strconv.Itoa(server.ServerPort)),
			RawQuery: rawQuery.Encode(),
			Fragment: server.Remarks,
		}
		obj, err := serverObj.NewFromLink("shadowsocks", u.String())
		if err != nil {
			return nil, SIP008{}, err
		}
		infos = append(infos, obj)
	}
	return
}

func resolveByLines(raw string) (infos []serverObj.ServerObj, status string, err error) {
	// Split raw
	rows := strings.Split(strings.TrimSpace(raw), "\n")
	// Parse
	infos = make([]serverObj.ServerObj, 0)
	for _, row := range rows {
		if strings.HasPrefix(row, "STATUS=") {
			status = strings.TrimPrefix(row, "STATUS=")
			continue
		}
		var data serverObj.ServerObj
		data, err = ResolveURL(row)
		if err != nil {
			if !errors.Is(err, EmptyAddressErr) {
				log.Warn("resolveByLines: %v: %v", err, row)
			}
			err = nil
			continue
		}
		infos = append(infos, data)
	}
	return
}

type SubscriptionUserInfo struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   time.Time
}

func (sui *SubscriptionUserInfo) String() string {
	var outputs []string
	if sui.Download != -1 {
		outputs = append(outputs, fmt.Sprintf("download: %v GB", sui.Download/1e9))
	}
	if sui.Upload != -1 {
		outputs = append(outputs, fmt.Sprintf("upload: %v GB", sui.Upload/1e9))
	}
	if sui.Total != -1 {
		outputs = append(outputs, fmt.Sprintf("total: %v GB", sui.Total/1e9))
	}
	if !sui.Expire.IsZero() {
		outputs = append(outputs, fmt.Sprintf("expire: %v UTC", sui.Expire.Format("2006-01-02 15:04")))
	}
	return strings.Join(outputs, "; ")
}

func parseSubscriptionUserInfo(str string) SubscriptionUserInfo {
	fields := strings.Split(str, ";")
	sui := SubscriptionUserInfo{
		Upload:   -1,
		Download: -1,
		Total:    -1,
		Expire:   time.Time{},
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		kv := strings.SplitN(field, "=", 2)
		if len(kv) < 2 {
			continue
		}
		v, e := strconv.ParseInt(kv[1], 10, 64)
		if e != nil {
			continue
		}
		switch kv[0] {
		case "upload":
			sui.Upload = v
		case "download":
			sui.Download = v
		case "total":
			sui.Total = v
		case "expire":
			sui.Expire = time.Unix(v, 0).UTC()
		}
	}
	return sui
}
func trapBOM(fileBytes []byte) []byte {
	trimmedBytes := bytes.Trim(fileBytes, "\xef\xbb\xbf")
	return trimmedBytes
}

// subscriptionFetchResult is the parsed result of fetching a subscription.
type subscriptionFetchResult struct {
	infos   []serverObj.ServerObj
	status  string
	traffic SubscriptionUserInfo
}

// ResolveSubscriptionWithClient downloads and parses a subscription. The
// per-subscription advanced options (User-Agent, X-HWID, download strategy,
// filters) come from extra; password is the decrypt password for encrypted
// subscriptions.
func ResolveSubscriptionWithClient(source string, client *http.Client, password string, extra configure.SubscriptionExtra) (result subscriptionFetchResult, err error) {
	c := *client
	if c.Timeout < 30*time.Second {
		c.Timeout = 30 * time.Second
	}

	// Per-subscription User-Agent.
	ua := "clash-verge"
	if extra.UserAgent != "" {
		ua = extra.UserAgent
	}
	if extra.UserAgentAppend {
		ua += " v2rayA/" + conf.Version
	}
	headers := map[string]string{}
	if extra.XHWID != "" {
		headers["X-HWID"] = extra.XHWID
	}

	// Download strategy: try the configured order of direct/proxy clients.
	clients, err := subscriptionDownloadClients(&c, extra)
	if err != nil {
		return
	}
	defer func() {
		for _, cl := range clients {
			if cl == nil || cl == client {
				continue
			}
			if tr, ok := cl.Transport.(*http.Transport); ok {
				tr.CloseIdleConnections()
			}
		}
	}()
	var res *http.Response
	for i, cl := range clients {
		res, err = httpClient.HttpGetUsingSpecificClientWithUAAndHeaders(cl, source, ua, headers)
		if err == nil {
			break
		}
		if i < len(clients)-1 {
			log.Warn("subscription download via client %d/%d failed: %v; trying next", i+1, len(clients), err)
		}
	}
	if err != nil {
		return
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return result, err
	}
	// Password-protected subscriptions (announced by the
	// "subscription-encryption" response header) must be decrypted before
	// parsing.
	if isSubscriptionEncrypted(res.Header) {
		var decrypted string
		decrypted, err = common.DecryptSubscriptionBody(b, password)
		if err != nil {
			return result, err
		}
		b = []byte(decrypted)
	}
	// base64 decode. trapBOM due to https://github.com/v2rayA/v2rayA/issues/612
	raw, err := common.Base64StdDecode(string(trapBOM(b)))
	if err != nil {
		raw, _ = common.Base64URLDecode(string(b))
	}
	// Many providers serve a full Clash YAML to clash-capable clients and a
	// reduced node list to others. Parse the YAML form when present.
	var infos []serverObj.ServerObj
	var status string
	if isClashYAML(raw) {
		infos, status, err = resolveClashYAML(raw)
	} else {
		infos, status, err = ResolveByLines(raw)
	}
	if err != nil {
		return result, err
	}
	infos = filterServersByExtra(infos, extra)
	subscriptionUserInfo := res.Header.Get("Subscription-Userinfo")
	sui := parseSubscriptionUserInfo(subscriptionUserInfo)
	if len(status) > 0 {
		status = sui.String() + "|" + status
	} else {
		status = sui.String()
	}
	return subscriptionFetchResult{infos: infos, status: status, traffic: sui}, nil
}

// subscriptionDownloadClients returns the HTTP clients to try in order,
// based on the per-subscription download strategy.
func subscriptionDownloadClients(direct *http.Client, extra configure.SubscriptionExtra) ([]*http.Client, error) {
	strategy := extra.DownloadStrategy
	if strategy == "" {
		// Follow the system: the default client already honors the
		// transparent proxy when active.
		return []*http.Client{direct}, nil
	}
	ports := configure.GetPortsNotNil()
	if ports.Socks5 <= 0 {
		switch strategy {
		case "onlyProxy":
			return nil, fmt.Errorf("download strategy is %q but the proxy port is not configured", strategy)
		default:
			return []*http.Client{direct}, nil
		}
	}
	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", ports.Socks5))
	if err != nil {
		return nil, fmt.Errorf("invalid proxy port %d: %w", ports.Socks5, err)
	}
	proxyClient := &http.Client{
		Timeout: direct.Timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	switch strategy {
	case "onlyProxy":
		return []*http.Client{proxyClient}, nil
	case "preferProxy":
		return []*http.Client{proxyClient, direct}, nil
	default:
		// preferDirect and anything else
		return []*http.Client{direct}, nil
	}
}

// filterServersByExtra applies the per-subscription include/exclude regexes
// and the "removed node" memory to a parsed node list.
func filterServersByExtra(infos []serverObj.ServerObj, extra configure.SubscriptionExtra) []serverObj.ServerObj {
	removed := make(map[string]bool, len(extra.RemovedTags))
	for _, t := range extra.RemovedTags {
		removed[t] = true
	}
	var includeRe, excludeRe *regexp.Regexp
	var err error
	if extra.IncludeRegex != "" {
		if includeRe, err = regexp.Compile(extra.IncludeRegex); err != nil {
			log.Warn("invalid include regex %q: %v", extra.IncludeRegex, err)
			includeRe = nil
		}
	}
	if extra.ExcludeRegex != "" {
		if excludeRe, err = regexp.Compile(extra.ExcludeRegex); err != nil {
			log.Warn("invalid exclude regex %q: %v", extra.ExcludeRegex, err)
			excludeRe = nil
		}
	}
	out := make([]serverObj.ServerObj, 0, len(infos))
	for _, info := range infos {
		name := info.GetName()
		if removed[name] {
			continue
		}
		if includeRe != nil && !includeRe.MatchString(name) {
			continue
		}
		if excludeRe != nil && excludeRe.MatchString(name) {
			continue
		}
		out = append(out, info)
	}
	return out
}

// applyServerFlags marks a server raw with its per-node flags (disabled,
// favorite) from the subscription extra.
func applyServerFlags(sr *configure.ServerRaw, extra configure.SubscriptionExtra) {
	if sr.ServerObj == nil {
		return
	}
	name := sr.ServerObj.GetName()
	for _, t := range extra.DisabledTags {
		if t == name {
			sr.Disabled = true
			break
		}
	}
	for _, t := range extra.FavTags {
		if t == name {
			sr.Fav = true
			break
		}
	}
}

// isSubscriptionEncrypted reports whether the response announces the
// "subscription-encryption" scheme (used by password-protected subscriptions).
func isSubscriptionEncrypted(h http.Header) bool {
	v := strings.TrimSpace(h.Get("subscription-encryption"))
	return strings.EqualFold(v, "true") || v == "1"
}

func ResolveByLines(raw string) (infos []serverObj.ServerObj, status string, err error) {
	var sip SIP008
	if infos, sip, err = resolveSIP008(raw); err == nil {
		status = getDataUsageStatus(sip.BytesUsed, sip.BytesRemaining)
	} else {
		infos, status, err = resolveByLines(raw)
	}
	return
}

func getDataUsageStatus(bytesUsed, bytesRemaining uint64) (status string) {
	if bytesUsed != 0 {
		status = fmt.Sprintf("Used: %.2f GiB", float64(bytesUsed)/1024/1024/1024)
		if bytesRemaining != 0 {
			status += fmt.Sprintf(" | Remaining: %.2f GiB", float64(bytesRemaining)/1024/1024/1024)
		}
	}
	return
}

func UpdateSubscription(index int, disconnectIfNecessary bool) (err error) {
	subscriptions := configure.GetSubscriptions()
	addr := subscriptions[index].Address
	password := subscriptions[index].DecryptPassword
	extra := subscriptions[index].Extra
	c := httpClient.GetHttpClientAutomatically()
	resolv.CheckResolvConf()
	result, err := ResolveSubscriptionWithClient(addr, c, password, extra)
	if err != nil {
		reason := "failed to resolve subscription address: " + err.Error()
		log.Warn("UpdateSubscription: %v: %v", err, result.infos)
		return fmt.Errorf("UpdateSubscription: %v", reason)
	}
	subscriptionInfos := result.infos
	status := result.status
	// Preserve latencies of nodes that keep the same name across updates so
	// users don't have to re-run tests after every refresh.
	oldNameLatency := make(map[string]string)
	for _, old := range subscriptions[index].Servers {
		if old.ServerObj != nil && old.Latency != "" {
			oldNameLatency[old.ServerObj.GetName()] = old.Latency
		}
	}
	infoServerRaws := make([]configure.ServerRaw, len(subscriptionInfos))
	css := configure.GetConnectedServers()
	cssAfter := css.Get()
	// serverObj.ServerObj is a pointer(interface), and shouldn't be as a key
	link2Raw := make(map[string]*configure.ServerRaw)
	connectedVmessInfo2CssIndex := make(map[string][]int)
	for i, cs := range css.Get() {
		if cs.TYPE == configure.SubscriptionServerType && cs.Sub == index {
			if sRaw, err := cs.LocateServerRaw(); err != nil {
				return err
			} else {
				if sRaw.ServerObj == nil {
					log.Warn("UpdateSubscription: skipping connected server with nil ServerObj (Sub=%d, ID=%d)", cs.Sub, cs.ID)
					continue
				}
				link := sRaw.ServerObj.ExportToURL()
				link2Raw[link] = sRaw
				connectedVmessInfo2CssIndex[link] = append(connectedVmessInfo2CssIndex[link], i)
			}
		}
	}
	// Replace list with new one, and find one with the same server value as the current connection, set it as Connected; if none, disconnect
	for i, info := range subscriptionInfos {
		infoServerRaw := configure.ServerRaw{
			ServerObj: info,
			Latency:   oldNameLatency[info.GetName()],
		}
		applyServerFlags(&infoServerRaw, extra)
		link := infoServerRaw.ServerObj.ExportToURL()
		if cssIndexes, ok := connectedVmessInfo2CssIndex[link]; ok {
			for _, cssIndex := range cssIndexes {
				cssAfter[cssIndex].ID = i + 1
			}
			delete(connectedVmessInfo2CssIndex, link)
		}
		infoServerRaws[i] = infoServerRaw
	}
	// Fallback: subscription providers often rename nodes (traffic/expiry counters in
	// names) while keeping the same endpoint. Remap remaining connected servers to new
	// servers with the same protocol, hostname and port.
	looseKey := func(obj serverObj.ServerObj) string {
		return obj.GetProtocol() + "://" + net.JoinHostPort(obj.GetHostname(), strconv.Itoa(obj.GetPort()))
	}
	loose2Index := make(map[string]int)
	for i, info := range subscriptionInfos {
		k := looseKey(info)
		if _, ok := loose2Index[k]; !ok {
			loose2Index[k] = i
		}
	}
	var connectedServerChanged bool
	for link, cssIndexes := range connectedVmessInfo2CssIndex {
		if i, ok := loose2Index[looseKey(link2Raw[link].ServerObj)]; ok {
			for _, cssIndex := range cssIndexes {
				cssAfter[cssIndex].ID = i + 1
			}
			connectedServerChanged = true
			log.Info("UpdateSubscription: remapped connected server %v to %v by endpoint match",
				link2Raw[link].ServerObj.GetName(), subscriptionInfos[i].GetName())
			delete(connectedVmessInfo2CssIndex, link)
		}
	}
	// Last fallback: some providers keep node names stable but rotate IPs, which
	// defeats the endpoint match. Remap by name, but only when the name maps to
	// exactly one server on each side to avoid connecting to a different node.
	name2Index := make(map[string]int)
	nameCount := make(map[string]int)
	for i, info := range subscriptionInfos {
		nameCount[info.GetName()]++
		name2Index[info.GetName()] = i
	}
	oldNameCount := make(map[string]int)
	for link := range connectedVmessInfo2CssIndex {
		oldNameCount[link2Raw[link].ServerObj.GetName()]++
	}
	for link, cssIndexes := range connectedVmessInfo2CssIndex {
		name := link2Raw[link].ServerObj.GetName()
		if nameCount[name] != 1 || oldNameCount[name] != 1 {
			continue
		}
		i := name2Index[name]
		for _, cssIndex := range cssIndexes {
			cssAfter[cssIndex].ID = i + 1
		}
		connectedServerChanged = true
		log.Info("UpdateSubscription: remapped connected server %v (%v -> %v) by unique name match",
			name, link2Raw[link].ServerObj.GetHostname(), subscriptionInfos[i].GetHostname())
		delete(connectedVmessInfo2CssIndex, link)
	}
	for link, cssIndexes := range connectedVmessInfo2CssIndex {
		for _, cssIndex := range cssIndexes {
			if disconnectIfNecessary {
				err = Disconnect(*css.Get()[cssIndex], false)
				if err != nil {
					reason := "failed to disconnect previous server"
					return fmt.Errorf("UpdateSubscription: %v", reason)
				}
			} else {
				// Append previously connected node
				infoServerRaws = append(infoServerRaws, *link2Raw[link])
				cssAfter[cssIndex].ID = len(infoServerRaws)
			}
		}
	}
	if err := configure.OverwriteConnects(configure.NewWhiches(cssAfter)); err != nil {
		return err
	}
	subscriptions[index].Servers = infoServerRaws
	subscriptions[index].Status = string(touch.NewUpdateStatus())
	subscriptions[index].Info = status
	subscriptions[index].Extra.LastUpdateTime = time.Now().Unix()
	subscriptions[index].Extra.Upload = result.traffic.Upload
	subscriptions[index].Extra.Download = result.traffic.Download
	subscriptions[index].Extra.Total = result.traffic.Total
	subscriptions[index].Extra.Expire = result.traffic.Expire.Unix()
	if err := configure.SetSubscription(index, &subscriptions[index]); err != nil {
		return err
	}
	// A remapped connection may point at a server whose config differs from the old
	// one; the running core keeps using the old config until it is regenerated.
	if connectedServerChanged && v2ray.ProcessManager.Running() {
		if err := v2ray.UpdateV2RayConfig(); err != nil {
			log.Warn("UpdateSubscription: failed to reload core after remapping connected servers: %v", err)
		}
	}
	return nil
}

func ModifySubscriptionRemark(subscription touch.Subscription) (err error) {
	raw := configure.GetSubscription(subscription.ID - 1)
	if raw == nil {
		return fmt.Errorf("failed to find the corresponding subscription")
	}
	raw.Remarks = subscription.Remarks
	raw.Address = subscription.Address
	raw.AutoSelect = subscription.AutoSelect
	raw.DecryptPassword = subscription.DecryptPassword
	raw.Extra = subscription.Extra
	if len(raw.Extra.RemovedTags) > 0 {
		removed := make(map[string]bool, len(raw.Extra.RemovedTags))
		for _, t := range raw.Extra.RemovedTags {
			removed[t] = true
		}
		// Map every current server ID of this subscription to its node name
		// while the original list is still intact.
		nameByOldID := make(map[int]string, len(raw.Servers))
		for i, srv := range raw.Servers {
			if srv.ServerObj != nil {
				nameByOldID[i+1] = srv.ServerObj.GetName()
			}
		}
		// Disconnect any connected server that is being removed.
		for _, cs := range configure.GetConnectedServers().Get() {
			if cs.TYPE == configure.SubscriptionServerType && cs.Sub == subscription.ID-1 {
				if name, ok := nameByOldID[cs.ID]; ok && removed[name] {
					if e := Disconnect(*cs, false); e != nil {
						log.Warn("ModifySubscriptionRemark: failed to disconnect removed server %v: %v", name, e)
					}
				}
			}
		}
		filtered := make([]configure.ServerRaw, 0, len(raw.Servers))
		for _, s := range raw.Servers {
			if s.ServerObj != nil && removed[s.ServerObj.GetName()] {
				continue
			}
			filtered = append(filtered, s)
		}
		raw.Servers = filtered
		// The removal shifted the server IDs of this subscription; remap the
		// remaining connected entries by name so they keep pointing at the
		// same nodes.
		cssAfter := configure.GetConnectedServers().Get()
		idToName := make(map[int]string, len(cssAfter))
		for _, cs := range cssAfter {
			if cs.TYPE == configure.SubscriptionServerType && cs.Sub == subscription.ID-1 {
				if name, ok := nameByOldID[cs.ID]; ok {
					idToName[cs.ID] = name
				}
			}
		}
		newIDByName := make(map[string]int, len(raw.Servers))
		for i, srv := range raw.Servers {
			if srv.ServerObj != nil {
				newIDByName[srv.ServerObj.GetName()] = i + 1
			}
		}
		for _, cs := range cssAfter {
			if cs.TYPE == configure.SubscriptionServerType && cs.Sub == subscription.ID-1 {
				if name, ok := idToName[cs.ID]; ok {
					if id, exist := newIDByName[name]; exist {
						cs.ID = id
					}
				}
			}
		}
		if err := configure.OverwriteConnects(configure.NewWhiches(cssAfter)); err != nil {
			log.Warn("ModifySubscriptionRemark: failed to update connections: %v", err)
		}
	}
	// Re-apply node flags (disabled/favorite) to the stored servers when the
	// corresponding tag lists change.
	for i := range raw.Servers {
		applyServerFlags(&raw.Servers[i], raw.Extra)
	}
	return configure.SetSubscription(subscription.ID-1, raw)
}

func SelectServersFromSubscription(index int, shouldDisconnect bool) (err error) {
	var subscriptionServer configure.Which
	subscriptionServer.TYPE = "subscriptionServer"
	subscriptionServer.Sub = index // Subscription IDs start with 0
	subscriptionServer.Outbound = "proxy"

	for i := 1; i < configure.GetLenSubscriptionServers(index)+1; i++ {
		subscriptionServer.ID = i // Server IDs start with 1
		sub := configure.GetSubscription(index)
		if sub == nil {
			return fmt.Errorf("SelectServersFromSubscription: subscription at index %d not found", index)
		}
		serverRaw := sub.Servers[i-1]
		if serverRaw.Disabled {
			log.Info("[AutoSelect] Skipping disabled server %v", serverRaw.ServerObj.GetName())
			continue
		}
		serverObj := serverRaw.ServerObj // ServerObj IDs start with 0
		if serverObj == nil {
			log.Warn("[AutoSelect] Skipping server %d in subscription %d: nil ServerObj", i, index)
			continue
		}
		serverName := serverObj.GetName()

		// Workaround for partial SS support in v2fly and xray
		isSupported, _ := IsSupported(subscriptionServer)
		if !isSupported {
			log.Info("[AutoSelect] Skipping unsupported server %v", serverName)
			continue
		}

		if shouldDisconnect {
			err := Disconnect(subscriptionServer, true)
			if err == nil {
				log.Info("[AutoSelect] Disconnected from server: %v", serverName)
			} else {
				log.Error("[AutoSelect] Failed to disconnect from server: %v", serverName)
				return err
			}
		} else {
			err := Connect(&subscriptionServer)
			if err == nil {
				log.Info("[AutoSelect] Automatically selected server: %v", serverName)
			} else {
				log.Error("[AutoSelect] Failed to connect to server: %v", serverName)
				return err
			}
		}
	}
	return nil
}

func AutoSelectServersFromSubscriptions(shouldDisconnect bool) (err error) {
	for i := 0; i < configure.GetLenSubscriptions(); i++ {
		subscription := configure.GetSubscription(i)
		if subscription == nil {
			log.Warn("[AutoSelect] Failed to read subscription at index %d, skipping", i)
			continue
		}
		if subscription.AutoSelect {
			if shouldDisconnect {
				log.Info("[AutoSelect] Automatically disconnecting servers from subscription: %v", subscription.Address)
				err := SelectServersFromSubscription(i, true)
				if err != nil {
					log.Error("[AutoSelect] Failed to disconnect servers from subscription: %v", subscription.Address)
					return err
				}
			} else {
				log.Info("[AutoSelect] Automatically selecting servers from subscription: %v", subscription.Address)
				err := SelectServersFromSubscription(i, false)
				if err != nil {
					log.Error("[AutoSelect] Failed to select servers from subscription: %v", subscription.Address)
					return err
				}
			}
		}
	}
	return nil
}
