package service

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/common/httpClient"
	"github.com/v2rayA/v2rayA/common/resolv"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/kernel/touch"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

type SIP008 struct {
	Version        int                   `json:"version"`
	Username       string                `json:"username"`
	UserUUID       string                `json:"user_uuid"`
	BytesUsed      uint64                `json:"bytes_used"`
	BytesRemaining uint64                `json:"bytes_remaining"`
	Servers        []jsoniter.RawMessage `json:"servers"`
}

type sip008Server struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
	Plugin     string `json:"plugin"`
	PluginOpts string `json:"plugin_opts"`
	Remarks    string `json:"remarks"`
}

func resolveSIP008(raw string, report *ImportResult) (infos []serverObj.ServerObj, sip SIP008, err error) {
	if jsoniter.Unmarshal([]byte(raw), &sip) != nil {
		return nil, sip, errors.New("invalid SIP008 profile metadata or servers list")
	}
	for index, data := range sip.Servers {
		var server sip008Server
		if jsoniter.Unmarshal(data, &server) != nil {
			report.skip(fmt.Sprintf("SIP008 server %d: invalid field type", index+1))
			continue
		}
		if server.Server == "" || server.ServerPort <= 0 || server.ServerPort > 65535 || server.Method == "" || server.Password == "" {
			report.skip(fmt.Sprintf("SIP008 server %d: missing address, port, cipher or password", index+1))
			continue
		}
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
			report.skip(fmt.Sprintf("SIP008 server %d: invalid Shadowsocks node", index+1))
			continue
		}
		infos = append(infos, obj)
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

// subscriptionFetchResult is the parsed result of fetching a subscription.
type subscriptionFetchResult struct {
	report  ImportResult
	infos   []serverObj.ServerObj
	status  string
	traffic SubscriptionUserInfo
	nodeDNS []string
}

// ResolveSubscriptionWithClient downloads and parses a subscription. The
// per-subscription advanced options (User-Agent, X-HWID, download strategy,
// filters) come from extra; password is the decrypt password for encrypted
// subscriptions.
func ResolveSubscriptionWithClient(source string, client *http.Client, password string, extra configure.SubscriptionExtra) (result subscriptionFetchResult, err error) {
	if err = extra.Validate(); err != nil {
		return result, err
	}
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
			if cl == nil || cl == &c {
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
		// The transport includes the subscription URL (and often its token) in
		// url.Error. Keep its underlying cause for diagnostics on all clients.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		if i < len(clients)-1 {
			log.Warn("subscription download via client %d/%d failed: %v; trying next", i+1, len(clients), err)
		}
	}
	if err != nil {
		return
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("subscription server returned HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return result, err
	}
	b, err = common.DecodeSubscriptionBody(b, res.Header.Get("Subscription-Encryption"), password, extra.AgePassword)
	if err != nil {
		return result, err
	}
	profile, err := parseProfile(string(b))
	if err != nil {
		return subscriptionFetchResult{report: profile.report}, err
	}
	infos := filterServersByExtra(profile.nodes, extra)
	if excluded := len(profile.nodes) - len(infos); excluded > 0 {
		profile.report.Imported = len(infos)
		profile.report.Skipped += excluded
		if len(profile.report.Reasons) < maxImportReasons {
			profile.report.Reasons = append(profile.report.Reasons, fmt.Sprintf("%d nodes excluded by subscription filters", excluded))
		}
	}
	status := profile.status
	subscriptionUserInfo := res.Header.Get("Subscription-Userinfo")
	sui := parseSubscriptionUserInfo(subscriptionUserInfo)
	if len(status) > 0 {
		status = sui.String() + "|" + status
	} else {
		status = sui.String()
	}
	return subscriptionFetchResult{infos: infos, status: status, traffic: sui, nodeDNS: profile.nodeDNS, report: profile.report}, nil
}

// subscriptionDownloadClients returns the HTTP clients to try in order,
// based on the per-subscription download strategy.
func subscriptionDownloadClients(system *http.Client, extra configure.SubscriptionExtra) ([]*http.Client, error) {
	strategy := extra.DownloadStrategy
	if strategy == "" {
		// Follow the system: the default client already honors the
		// transparent proxy when active.
		return []*http.Client{system}, nil
	}
	direct := httpClient.GetHttpClientDirect()
	direct.Timeout = system.Timeout
	if strategy == "onlyDirect" {
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
	case "preferDirect":
		return []*http.Client{direct, proxyClient}, nil
	default:
		return nil, fmt.Errorf("invalid subscription download strategy %q", strategy)
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
		if info == nil {
			continue
		}
		name := info.GetName()
		if len(extra.IncludeProtocols) > 0 {
			found := false
			for _, protocol := range extra.IncludeProtocols {
				if protocol == info.GetProtocol() {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		target := name
		if extra.FilterField == "host" {
			target = info.GetHostname()
		}
		if removed[name] {
			continue
		}
		if includeRe != nil && !includeRe.MatchString(target) {
			continue
		}
		if excludeRe != nil && excludeRe.MatchString(target) {
			continue
		}
		out = append(out, info)
	}
	return out
}

// applyServerFlags marks a server raw with its per-node flags (disabled,
// favorite) from the subscription extra.
func applyServerFlags(sr *configure.ServerRaw, extra configure.SubscriptionExtra) {
	sr.Disabled, sr.Fav = false, false
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

func ResolveByLines(raw string) (infos []serverObj.ServerObj, status string, err error) {
	p, err := parseProfile(raw)
	return p.nodes, p.status, err
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
	membershipMu.Lock()
	source := configure.GetSubscription(index)
	membershipMu.Unlock()
	if source == nil {
		return fmt.Errorf("subscription not found")
	}
	addr := source.Address
	password := source.DecryptPassword
	extra := source.Extra
	c := httpClient.GetHttpClientAutomatically()
	resolv.CheckResolvConf()
	result, err := ResolveSubscriptionWithClient(addr, c, password, extra)
	if err != nil {
		reason := "failed to resolve subscription address: " + err.Error()
		log.Warn("UpdateSubscription: %v: %v", err, result.infos)
		return fmt.Errorf("UpdateSubscription: %v", reason)
	}
	membershipMu.Lock()
	defer membershipMu.Unlock()
	subscriptions, err := configure.ReadSubscriptions()
	if err != nil {
		return err
	}
	if index >= len(subscriptions) {
		return fmt.Errorf("subscription removed during download")
	}
	oldRaw := subscriptions[index]
	if oldRaw.DatabaseID != source.DatabaseID || oldRaw.Address != addr || oldRaw.DecryptPassword != password ||
		!reflect.DeepEqual(oldRaw.Extra, extra) || !reflect.DeepEqual(oldRaw.Servers, source.Servers) {
		return fmt.Errorf("subscription changed during download; retry the refresh")
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
	groups := configure.GetOutbounds()
	previous := make(map[string]*configure.Whiches, len(groups))
	var connections []*configure.Which
	for _, group := range groups {
		previous[group] = configure.GetConnectedServersByOutbound(group)
		for _, w := range previous[group].Get() {
			copy := *w
			connections = append(connections, &copy)
		}
	}
	for i, info := range subscriptionInfos {
		raw := configure.ServerRaw{ServerObj: info, Latency: oldNameLatency[info.GetName()]}
		applyServerFlags(&raw, extra)
		infoServerRaws[i] = raw
	}
	ruleMembers, err := refreshRuleMembers(index, subscriptions, infoServerRaws, previous)
	if err != nil {
		return err
	}
	cssAfter := make([]*configure.Which, 0, len(connections))
	// Nonempty rules replace the entire group. Zero matches keep the old
	// endpoint exactly, even if a different node now occupies its position.
	retained := map[string]int{}
	for i, n := range infoServerRaws {
		retained[n.ServerObj.ExportToURL()] = i + 1
	}
	standalone := standaloneMembers{}
	for _, w := range connections {
		nodes, managed := ruleMembers[w.Outbound]
		if managed && (len(nodes) > 0 || !enabledMember(w, subscriptions, standalone)) {
			continue
		}
		if managed && w.TYPE == configure.SubscriptionServerType && w.Sub == index {
			n := oldRaw.Servers[w.ID-1]
			link := n.ServerObj.ExportToURL()
			id := retained[link]
			if id > 0 && !enabledNode(&infoServerRaws[id-1], &extra) {
				continue
			}
			if id == 0 {
				n.Retained = true
				infoServerRaws = append(infoServerRaws, n)
				id = len(infoServerRaws)
				retained[link] = id
			}
			w.ID = id
		}
		cssAfter = append(cssAfter, w)
	}
	// serverObj.ServerObj is a pointer(interface), and shouldn't be as a key
	link2Raw := make(map[string]*configure.ServerRaw)
	connectedVmessInfo2CssIndex := make(map[string][]int)
	for i, cs := range cssAfter {
		if _, managed := ruleMembers[cs.Outbound]; managed {
			continue
		}
		if cs.TYPE != configure.SubscriptionServerType || cs.Sub != index {
			continue
		}
		if cs.ID < 1 || cs.ID > len(oldRaw.Servers) || oldRaw.Servers[cs.ID-1].ServerObj == nil {
			return fmt.Errorf("invalid saved member in %s", cs.Outbound)
		}
		raw := &oldRaw.Servers[cs.ID-1]
		link := raw.ServerObj.ExportToURL()
		link2Raw[link] = raw
		connectedVmessInfo2CssIndex[link] = append(connectedVmessInfo2CssIndex[link], i)
	}
	// Replace list with new one, and find one with the same server value as the current connection, set it as Connected; if none, disconnect
	for i, info := range subscriptionInfos {
		link := info.ExportToURL()
		if cssIndexes, ok := connectedVmessInfo2CssIndex[link]; ok {
			for _, cssIndex := range cssIndexes {
				cssAfter[cssIndex].ID = i + 1
			}
			delete(connectedVmessInfo2CssIndex, link)
		}
	}
	// Fallback: subscription providers often rename nodes (traffic/expiry counters in
	// names) while keeping the same endpoint. Remap remaining connected servers to new
	// servers with the same protocol, hostname and port.
	looseKey := func(obj serverObj.ServerObj) string {
		return obj.GetProtocol() + "://" + net.JoinHostPort(obj.GetHostname(), strconv.Itoa(obj.GetPort()))
	}
	// A single endpoint may serve several independent credentials. Only use
	// the endpoint alone when it identifies one node in both subscriptions.
	oldEndpointCount := make(map[string]int)
	for _, server := range subscriptions[index].Servers {
		if server.ServerObj != nil {
			oldEndpointCount[looseKey(server.ServerObj)]++
		}
	}
	loose2Index := make(map[string]int)
	for i, info := range subscriptionInfos {
		k := looseKey(info)
		if _, exists := loose2Index[k]; exists {
			loose2Index[k] = -1
		} else {
			loose2Index[k] = i
		}
	}
	var connectedServerChanged bool
	for link, cssIndexes := range connectedVmessInfo2CssIndex {
		key := looseKey(link2Raw[link].ServerObj)
		if i, ok := loose2Index[key]; ok && i >= 0 && oldEndpointCount[key] == 1 {
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
		id := retained[link]
		if !disconnectIfNecessary && id == 0 {
			n := *link2Raw[link]
			n.Retained = true
			infoServerRaws = append(infoServerRaws, n)
			id = len(infoServerRaws)
		}
		for _, cssIndex := range cssIndexes {
			if disconnectIfNecessary {
				cssAfter[cssIndex] = nil
				connectedServerChanged = true
			} else {
				cssAfter[cssIndex].ID = id
			}
		}
	}
	grouped := map[string]*configure.Whiches{}
	for _, group := range groups {
		grouped[group] = configure.NewWhiches(nil)
	}
	for _, w := range cssAfter {
		if w != nil {
			grouped[w.Outbound].Add(*w)
		}
	}
	for group, nodes := range ruleMembers {
		if len(nodes) > 0 {
			grouped[group] = configure.NewWhiches(nodes)
		}
	}
	nextStates := map[string]any{}
	oldStates := map[string]any{}
	for group, members := range grouped {
		prev := previous[group]
		if !sameMembers(members.Get(), prev.Get()) {
			oldStates[memberKey(group)] = prev
			nextStates[memberKey(group)] = members
			connectedServerChanged = true
		}
		// A provider can replace a node at the very same list position.
		if _, managed := ruleMembers[group]; managed && memberEndpointsChanged(members.Get(), index, oldRaw.Servers, infoServerRaws) {
			connectedServerChanged = true
		}
	}
	subscriptions[index].Servers = infoServerRaws
	subscriptions[index].Status = string(touch.NewUpdateStatus())
	subscriptions[index].Info = status
	subscriptions[index].Extra.LastUpdateTime = time.Now().Unix()
	dnsChanged := strings.Join(subscriptions[index].Extra.ProviderNodeDNS, "\n") != strings.Join(result.nodeDNS, "\n")
	subscriptions[index].Extra.ProviderNodeDNS = result.nodeDNS
	subscriptions[index].Extra.Upload = result.traffic.Upload
	subscriptions[index].Extra.Download = result.traffic.Download
	subscriptions[index].Extra.Total = result.traffic.Total
	subscriptions[index].Extra.Expire = result.traffic.Expire.Unix()
	return publishSubscriptionMembers(index, &oldRaw, &subscriptions[index], oldStates, nextStates, connectedServerChanged || dnsChanged)
}

func memberEndpointsChanged(members []*configure.Which, index int, before, after []configure.ServerRaw) bool {
	for _, w := range members {
		if w.TYPE != configure.SubscriptionServerType || w.Sub != index {
			continue
		}
		pos := w.ID - 1
		if pos < 0 || pos >= len(before) || pos >= len(after) || before[pos].ServerObj == nil || after[pos].ServerObj == nil ||
			before[pos].ServerObj.ExportToURL() != after[pos].ServerObj.ExportToURL() {
			return true
		}
	}
	return false
}

// Positional node references and the provider rows they address must be
// published and rolled back in the same transaction.
func publishSubscriptionMembers(index int, old, updated *configure.SubscriptionRaw, oldStates, nextStates map[string]any, reload bool) error {
	if err := db.SetSubscriptionAndState(index, updated, nextStates); err != nil {
		return err
	}
	if reload && v2ray.ProcessManager.Running() {
		if err := v2ray.UpdateV2RayConfig(); err != nil {
			err = errors.Join(err, db.SetSubscriptionAndState(index, old, oldStates))
			if !v2ray.ProcessManager.Running() {
				err = errors.Join(err, v2ray.UpdateV2RayConfig())
			}
			return err
		}
	}
	return nil
}

func ModifySubscriptionRemark(subscription touch.Subscription) error {
	membershipMu.Lock()
	defer membershipMu.Unlock()
	if err := subscription.Extra.Validate(); err != nil {
		return err
	}
	index := subscription.ID - 1
	previous := configure.GetSubscription(index)
	if previous == nil {
		return fmt.Errorf("failed to find the corresponding subscription")
	}
	updated := *previous

	// Provider data comes from the downloaded profile, never from the editor.
	subscription.Extra.ProviderNodeDNS = previous.Extra.ProviderNodeDNS
	// The overview omits AGE identities. An ordinary metadata edit must keep
	// the saved credential; an explicit empty value clears it.
	subscription.Extra.AgePassword = previous.Extra.AgePassword
	if subscription.AgePassword != nil {
		subscription.Extra.AgePassword = *subscription.AgePassword
	}
	reload := !slices.Equal(previous.Extra.EffectiveNodeDNS(), subscription.Extra.EffectiveNodeDNS())
	updated.Extra = subscription.Extra
	updated.Remarks = subscription.Remarks
	updated.Address = subscription.Address
	updated.AutoSelect = subscription.AutoSelect
	updated.DecryptPassword = subscription.DecryptPassword

	membershipChanged := !slices.Equal(previous.Extra.DisabledTags, updated.Extra.DisabledTags) ||
		!slices.Equal(previous.Extra.RemovedTags, updated.Extra.RemovedTags)
	// Preserve identity even when two different nodes share a display name.
	newIDs := make([]int, len(previous.Servers)+1)
	updated.Servers = make([]configure.ServerRaw, 0, len(previous.Servers))
	for i, node := range previous.Servers {
		if node.ServerObj != nil && slices.Contains(updated.Extra.RemovedTags, node.ServerObj.GetName()) {
			membershipChanged = true
			continue
		}
		wasDisabled := node.Disabled
		applyServerFlags(&node, updated.Extra)
		if wasDisabled != node.Disabled {
			membershipChanged = true
		}
		updated.Servers = append(updated.Servers, node)
		newIDs[i+1] = len(updated.Servers)
	}
	// Earlier versions could leave a disabled node selected. Repeating a
	// PATCH with the same flags should repair only groups still using it.
	if !membershipChanged && len(updated.Extra.DisabledTags) > 0 {
		for _, group := range configure.GetOutbounds() {
			if configure.GetOutboundSetting(group).MemberRule == nil {
				continue
			}
			old := configure.GetConnectedServersByOutbound(group).Get()
			if slices.ContainsFunc(old, func(w *configure.Which) bool {
				return w.TYPE == configure.SubscriptionServerType && w.Sub == index && w.ID > 0 &&
					w.ID <= len(updated.Servers) && updated.Servers[w.ID-1].Disabled
			}) {
				membershipChanged = true
				break
			}
		}
	}
	if !membershipChanged {
		return publishSubscriptionMembers(index, previous, &updated, nil, nil, reload)
	}
	var subs []configure.SubscriptionRaw
	standalone := standaloneMembers{}
	oldStates, nextStates := map[string]any{}, map[string]any{}
	for _, group := range configure.GetOutbounds() {
		old := configure.GetConnectedServersByOutbound(group)
		rule := configure.GetOutboundSetting(group).MemberRule
		remapped := make([]*configure.Which, 0, old.Len())
		for _, w := range old.Get() {
			if w.TYPE != configure.SubscriptionServerType || w.Sub != index {
				remapped = append(remapped, w)
				continue
			}
			if w.ID < 1 || w.ID >= len(newIDs) || newIDs[w.ID] == 0 {
				continue
			}
			member := *w
			member.ID = newIDs[w.ID]
			remapped = append(remapped, &member)
		}
		if affectedRule(rule, updated.DatabaseID, index, old.Get()) {
			if subs == nil {
				var err error
				subs, err = configure.ReadSubscriptions()
				if err != nil {
					return err
				}
				if index >= len(subs) || subs[index].DatabaseID != updated.DatabaseID {
					return fmt.Errorf("subscription changed during edit")
				}
				subs[index] = updated
			}
			matches, err := matchOutboundMembers(group, *rule, subs)
			if err != nil {
				return err
			}
			remapped = effectiveMembers(matches, remapped, subs, standalone)
		}
		if !sameMembers(old.Get(), remapped) {
			oldStates[memberKey(group)] = old
			nextStates[memberKey(group)] = configure.NewWhiches(remapped)
			reload = true
		} else if memberEndpointsChanged(remapped, index, previous.Servers, updated.Servers) {
			reload = true
		}
	}
	return publishSubscriptionMembers(index, previous, &updated, oldStates, nextStates, reload)
}

func SelectServersFromSubscription(index int, shouldDisconnect bool) (err error) {
	membershipMu.Lock()
	defer membershipMu.Unlock()
	if configure.GetOutboundSetting("proxy").MemberRule != nil {
		return nil
	}
	var subscriptionServer configure.Which
	subscriptionServer.TYPE = "subscriptionServer"
	subscriptionServer.Sub = index // Subscription IDs start with 0
	subscriptionServer.Outbound = "proxy"

	sub := configure.GetSubscription(index)
	if sub == nil {
		return fmt.Errorf("SelectServersFromSubscription: subscription at index %d not found", index)
	}
	for i, serverRaw := range sub.Servers {
		subscriptionServer.ID = i + 1
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
			err := disconnect(subscriptionServer, true)
			if err == nil {
				log.Info("[AutoSelect] Disconnected from server: %v", serverName)
			} else {
				log.Error("[AutoSelect] Failed to disconnect from server: %v", serverName)
				return err
			}
		} else {
			err := connect(&subscriptionServer)
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

// PreviewSubscriptionFilter never downloads a profile or alters stored nodes.
func PreviewSubscriptionFilter(id int, extra configure.SubscriptionExtra) (int, int, error) {
	if err := extra.Validate(); err != nil {
		return 0, 0, err
	}
	if id < 1 || id > configure.GetLenSubscriptions() {
		return 0, 0, fmt.Errorf("subscription not found")
	}
	raw := configure.GetSubscription(id - 1)
	var nodes []serverObj.ServerObj
	for _, node := range raw.Servers {
		if node.ServerObj != nil {
			nodes = append(nodes, node.ServerObj)
		}
	}
	return len(nodes), len(filterServersByExtra(nodes, extra)), nil
}
