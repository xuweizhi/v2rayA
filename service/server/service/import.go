package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/pkg/util/log"

	"github.com/v2rayA/v2rayA/common/httpClient"
	"github.com/v2rayA/v2rayA/common/resolv"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/kernel/touch"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

func PluginManagerValidateLink(url string) bool {
	if pm := conf.GetEnvironmentConfig().PluginManager; pm != "" {
		_, err := serverObj.NewFromLink(serverObj.PluginManagerScheme, url)
		return err == nil
	} else {
		return false
	}
}

type ImportRequest struct {
	URL         string           `json:"url"`
	Which       *configure.Which `json:"which"`
	Password    string           `json:"password"`
	AgePassword string           `json:"agePassword"`
	Content     bool             `json:"content"`
	Remarks     *string          `json:"remarks"`
	NodeDNS     []string         `json:"nodeDNS"`
}

// Import is also used by database migration at startup.
func Import(source string, which *configure.Which, password string) error {
	_, err := ImportProfile(ImportRequest{URL: source, Which: which, Password: password})
	return err
}

func ImportProfile(req ImportRequest) (ImportResult, error) {
	var report ImportResult
	resolv.CheckResolvConf()
	source := strings.TrimSpace(req.URL)
	if source == "" {
		return report, errors.New("empty profile")
	}
	if isProfileContent(req, source) {
		profile, err := parseProfile(source)
		if err != nil {
			return profile.report, err
		}
		if req.Which != nil && req.Which.ID > 0 {
			if len(profile.nodes) != 1 || profile.report.Skipped > 0 {
				return report, errors.New("editing a node requires exactly one valid node")
			}
			return profile.report, updateImportedServer(req.Which, profile.nodes[0])
		}
		nodes := make([]*configure.ServerRaw, 0, len(profile.nodes))
		for _, node := range profile.nodes {
			nodes = append(nodes, &configure.ServerRaw{ServerObj: node})
		}
		if err := configure.AppendServers(nodes); err != nil {
			return report, fmt.Errorf("append imported nodes: %w", err)
		}
		AutoBackup()
		return profile.report, nil
	}
	if req.Which != nil && req.Which.ID > 0 {
		return report, errors.New("editing a node requires a node link")
	}
	link, err := ParseImportLink(source)
	if err != nil {
		return report, err
	}
	if req.Remarks != nil {
		link.Remarks = *req.Remarks
	}
	if req.NodeDNS != nil {
		link.NodeDNS = req.NodeDNS
	}
	extra := configure.SubscriptionExtra{NodeDNS: link.NodeDNS, AgePassword: req.AgePassword}
	if err := extra.Validate(); err != nil {
		return report, errors.New("invalid subscription DNS options")
	}
	client := *httpClient.GetHttpClientAutomatically()
	client.Timeout = 90 * time.Second
	result, err := ResolveSubscriptionWithClient(link.URL, &client, req.Password, extra)
	if err != nil {
		return result.report, fmt.Errorf("failed to resolve subscription address: %w", err)
	}
	servers := make([]configure.ServerRaw, len(result.infos))
	for i, node := range result.infos {
		servers[i] = configure.ServerRaw{ServerObj: node}
	}
	extra.Upload = result.traffic.Upload
	extra.Download = result.traffic.Download
	extra.Total = result.traffic.Total
	extra.Expire = result.traffic.Expire.Unix()
	extra.LastUpdateTime = time.Now().Unix()
	extra.ProviderNodeDNS = result.nodeDNS
	extra.PreferNodeDNS = len(extra.NodeDNS) > 0 || (req.NodeDNS == nil && len(result.nodeDNS) > 0)
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{
		Address: link.URL, Remarks: link.Remarks, Status: string(touch.NewUpdateStatus()),
		Servers: servers, Info: result.status, DecryptPassword: req.Password, Extra: extra,
	}}); err != nil {
		return report, err
	}
	AutoBackup()
	return result.report, nil
}

func isProfileContent(req ImportRequest, source string) bool {
	if req.Content || strings.Contains(source, "\n") || strings.HasPrefix(source, "{") ||
		strings.HasPrefix(source, "[") || isClashYAML(source) {
		return true
	}
	if scheme, _, found := strings.Cut(source, "://"); found {
		scheme = strings.ToLower(scheme)
		if isInstallScheme(scheme) || scheme == "sub" {
			return false
		}
		if scheme == "http" || scheme == "https" {
			return PluginManagerValidateLink(source)
		}
		return true
	}
	// A single base64-encoded list pasted into the normal input is content,
	// whereas a bare hostname/path is a legacy scheme-less subscription URL.
	decoded := decodeProfileText(source)
	return strings.Contains(decoded, "://") || strings.HasPrefix(decoded, "{") || isClashYAML(decoded)
}

func updateImportedServer(which *configure.Which, obj serverObj.ServerObj) (err error) {
	membershipMu.Lock()
	defer membershipMu.Unlock()
	ind := which.ID - 1
	log.Info("Import: modifying server. ind=%v, which.ID=%v, which.TYPE=%v", ind, which.ID, which.TYPE)
	if which.TYPE != configure.ServerType {
		// Also support modifying subscription servers if the frontend allows it
		// but for now, we primarily care about ServerType
		if which.TYPE != configure.SubscriptionServerType {
			log.Warn("Import: unsupported touch type for modification: %v", which.TYPE)
			return fmt.Errorf("bad request: unsupported touch type")
		}
	}

	if which.TYPE == configure.ServerType && (ind < 0 || ind >= configure.GetLenServers()) {
		log.Warn("Import: invalid server index: %v", ind)
		return fmt.Errorf("bad request: invalid index")
	}

	old, locateErr := which.LocateServerRaw()
	if locateErr != nil {
		return locateErr
	}
	updated := *old
	updated.ServerObj = obj
	updated.Latency = ""
	if err = setImportedServer(*which, &updated); err != nil {
		return
	}
	log.Info("Import: SetServer success for index %v", ind)
	css := configure.GetConnectedServers()
	if v2ray.ProcessManager.Running() && css.Len() > 0 {
		for _, cs := range css.Get() {
			if which.TYPE == cs.TYPE && which.ID == cs.ID && (which.TYPE != configure.SubscriptionServerType || which.Sub == cs.Sub) {
				log.Info("Import: updating connected v2ray config")
				if err = v2ray.UpdateV2RayConfig(); err != nil {
					err = errors.Join(err, setImportedServer(*which, old))
					if !v2ray.ProcessManager.Running() {
						err = errors.Join(err, v2ray.UpdateV2RayConfig())
					}
					return
				}
				break
			}
		}
	}
	return nil
}

func setImportedServer(which configure.Which, server *configure.ServerRaw) error {
	if which.TYPE == configure.ServerType {
		return configure.SetServer(which.ID-1, server)
	}
	if which.TYPE != configure.SubscriptionServerType {
		return fmt.Errorf("unsupported node type")
	}
	sub := configure.GetSubscription(which.Sub)
	if sub == nil || which.ID < 1 || which.ID > len(sub.Servers) {
		return fmt.Errorf("subscription node not found")
	}
	sub.Servers[which.ID-1] = *server
	return configure.SetSubscription(which.Sub, sub)
}
