package service

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

// Timer refreshes bypass the HTTP controller's update guard.
var membershipMu sync.Mutex

type SubscriptionSource struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

func SubscriptionSources() ([]SubscriptionSource, error) {
	rows, err := db.GetDB().Query("SELECT id, remarks, address FROM subscriptions ORDER BY sort")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := []SubscriptionSource{}
	for rows.Next() {
		var source SubscriptionSource
		var address string
		if err := rows.Scan(&source.ID, &source.Label, &address); err != nil {
			return nil, err
		}
		if source.Label == "" || strings.Contains(source.Label, "://") {
			source.Label = ""
			if u, err := url.Parse(address); err == nil {
				source.Label = u.Hostname()
			}
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func matchOutboundMembers(group string, rule configure.OutboundMemberRule, subs []configure.SubscriptionRaw) ([]*configure.Which, error) {
	if len(rule.SubscriptionIDs) == 0 {
		return nil, errors.New("select at least one source subscription")
	}
	pattern := strings.TrimSpace(rule.Pattern)
	if pattern == "" {
		return nil, errors.New("a node name pattern is required")
	}
	var matches func(string) bool
	switch rule.Mode {
	case "keyword":
		keyword := strings.ToLower(pattern)
		matches = func(name string) bool { return strings.Contains(strings.ToLower(name), keyword) }
	case "regex":
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		matches = re.MatchString
	default:
		return nil, errors.New("invalid node name match mode")
	}
	var members []*configure.Which
	for i, sub := range subs {
		if !slices.Contains(rule.SubscriptionIDs, sub.DatabaseID) {
			continue
		}
		for j, s := range sub.Servers {
			if !s.Retained && enabledNode(&s, &sub.Extra) && matches(s.ServerObj.GetName()) {
				members = append(members, &configure.Which{ID: j + 1, TYPE: configure.SubscriptionServerType, Sub: i, Outbound: group})
			}
		}
	}
	return members, nil
}

type MemberPreview struct {
	Members      []string `json:"members"`
	Matched      int      `json:"matched"`
	KeepPrevious bool     `json:"keepPrevious"`
	Added        []string `json:"added"`
	Removed      []string `json:"removed"`
}

type memberPosition struct {
	kind      configure.TouchType
	sub, node int
}

// Standalone nodes can be former manual members. Look up only those positions
// and cache them across fallback checks and preview lists.
type standaloneMembers map[int]*configure.ServerRaw

func (m standaloneMembers) get(id int) *configure.ServerRaw {
	if id < 1 {
		return nil
	}
	if node, ok := m[id]; ok {
		return node
	}
	var node *configure.ServerRaw
	row, err := db.ListGet("touch", "servers", id-1)
	if err == nil {
		node, _ = configure.Bytes2ServerRaw(row)
	}
	m[id] = node
	return node
}

func memberNode(w *configure.Which, subs []configure.SubscriptionRaw, standalone standaloneMembers) (*configure.ServerRaw, *configure.SubscriptionRaw) {
	if w.TYPE == configure.ServerType {
		return standalone.get(w.ID), nil
	}
	if w.TYPE != configure.SubscriptionServerType || w.Sub < 0 || w.Sub >= len(subs) {
		return nil, nil
	}
	src := &subs[w.Sub]
	if w.ID < 1 || w.ID > len(src.Servers) {
		return nil, src
	}
	return &src.Servers[w.ID-1], src
}

func memberNames(members, exclude []*configure.Which, subscriptions []configure.SubscriptionRaw, standalone standaloneMembers) []string {
	skip := make(map[memberPosition]bool, len(exclude))
	for _, w := range exclude {
		skip[memberPosition{w.TYPE, w.Sub, w.ID}] = true
	}
	names := make([]string, 0, len(members))
	for _, w := range members {
		if skip[memberPosition{w.TYPE, w.Sub, w.ID}] {
			continue
		}
		node, source := memberNode(w, subscriptions, standalone)
		if node == nil || node.ServerObj == nil {
			continue
		}
		name := node.ServerObj.GetName()
		if source != nil {
			name = fmt.Sprintf("#%d · %s", source.DatabaseID, name)
		}
		names = append(names, name)
	}
	return names
}

// Retention can keep a node that vanished from its provider, but an explicit
// disable/remove always wins, including for a saved pre-rule manual member.
func enabledNode(node *configure.ServerRaw, extra *configure.SubscriptionExtra) bool {
	if node == nil || node.ServerObj == nil || node.Disabled {
		return false
	}
	if extra == nil {
		return true
	}
	name := node.ServerObj.GetName()
	return !slices.Contains(extra.DisabledTags, name) && !slices.Contains(extra.RemovedTags, name)
}

func enabledMember(w *configure.Which, subs []configure.SubscriptionRaw, standalone standaloneMembers) bool {
	node, source := memberNode(w, subs, standalone)
	if source != nil {
		return enabledNode(node, &source.Extra)
	}
	return enabledNode(node, nil)
}

func effectiveMembers(matches, previous []*configure.Which, subs []configure.SubscriptionRaw, servers standaloneMembers) []*configure.Which {
	if len(matches) > 0 {
		return matches
	}
	var kept []*configure.Which
	for _, w := range previous {
		if enabledMember(w, subs, servers) {
			kept = append(kept, w)
		}
	}
	return kept
}

func sameMembers(a, b []*configure.Which) bool {
	return slices.EqualFunc(a, b, func(x, y *configure.Which) bool { return x.EqualTo(*y) })
}

func resolveRequestedRule(group string, rule configure.OutboundMemberRule, subscriptions []configure.SubscriptionRaw) ([]*configure.Which, error) {
	for _, id := range rule.SubscriptionIDs {
		if !slices.ContainsFunc(subscriptions, func(s configure.SubscriptionRaw) bool { return s.DatabaseID == id }) {
			return nil, fmt.Errorf("source subscription #%d no longer exists", id)
		}
	}
	return matchOutboundMembers(group, rule, subscriptions)
}

func PreviewOutboundMembers(group string, rule configure.OutboundMemberRule) (MemberPreview, error) {
	membershipMu.Lock()
	defer membershipMu.Unlock()
	if !outboundExists(group) {
		return MemberPreview{}, errors.New("outbound group does not exist")
	}
	subs, err := configure.ReadSubscriptions()
	if err != nil {
		return MemberPreview{}, err
	}
	matches, err := resolveRequestedRule(group, rule, subs)
	if err != nil {
		return MemberPreview{}, err
	}
	old := configure.GetConnectedServersByOutbound(group).Get()
	servers := standaloneMembers{}
	effective := effectiveMembers(matches, old, subs, servers)
	preview := MemberPreview{Matched: len(matches), KeepPrevious: len(matches) == 0 && len(effective) > 0}
	preview.Members = memberNames(effective, nil, subs, servers)
	preview.Added = memberNames(effective, old, subs, servers)
	preview.Removed = memberNames(old, effective, subs, servers)
	return preview, nil
}

func memberKey(group string) string { return "outbound." + group + ":connectedServers" }

// ApplyOutboundSetting saves a rule and its initial selection together. Turning
// off the rule leaves its last selection available for manual editing.
func ApplyOutboundSetting(group string, setting configure.OutboundSetting) error {
	membershipMu.Lock()
	defer membershipMu.Unlock()
	if err := configure.ValidateOutboundSetting(group, setting); err != nil {
		return err
	}
	setKey := "outbound." + group + ":setting"
	next := map[string]any{setKey: setting}
	old := map[string]any{setKey: configure.GetOutboundSetting(group)}
	if rule := setting.MemberRule; rule != nil {
		subs, err := configure.ReadSubscriptions()
		if err != nil {
			return err
		}
		matches, err := resolveRequestedRule(group, *rule, subs)
		if err != nil {
			return err
		}
		previous := configure.GetConnectedServersByOutbound(group)
		effective := effectiveMembers(matches, previous.Get(), subs, standaloneMembers{})
		if !sameMembers(previous.Get(), effective) {
			next[memberKey(group)] = configure.NewWhiches(effective)
			old[memberKey(group)] = previous
		}
	}
	return applyOutboundState(next, old)
}

// The caller holds membershipMu across persistence, reload and recovery.
func applyOutboundState(next, old map[string]any) error {
	if err := db.SetState(next); err != nil {
		return err
	}
	if v2ray.ProcessManager.Process() != nil {
		if err := v2ray.UpdateV2RayConfig(); err != nil {
			err = errors.Join(err, db.SetState(old))
			if v2ray.ProcessManager.Process() == nil {
				err = errors.Join(err, v2ray.UpdateV2RayConfig())
			}
			return fmt.Errorf("reloading outbound settings: %w", err)
		}
	}
	return nil
}

// A previous manual member can remain active when the rule selects a
// different source with zero matches. Refreshes and edits to that former
// source must also reconcile the group.
func affectedRule(rule *configure.OutboundMemberRule, sourceID int64, index int, previous []*configure.Which) bool {
	return rule != nil && (slices.Contains(rule.SubscriptionIDs, sourceID) ||
		slices.ContainsFunc(previous, func(w *configure.Which) bool { return w.TYPE == configure.SubscriptionServerType && w.Sub == index }))
}

// refreshRuleMembers matches against newly published nodes before adding any
// retained endpoints for existing connections.
func refreshRuleMembers(index int, subs []configure.SubscriptionRaw, fresh []configure.ServerRaw, previous map[string]*configure.Whiches) (map[string][]*configure.Which, error) {
	view := slices.Clone(subs)
	view[index].Servers = fresh
	out := map[string][]*configure.Which{}
	for group, old := range previous {
		rule := configure.GetOutboundSetting(group).MemberRule
		if !affectedRule(rule, subs[index].DatabaseID, index, old.Get()) {
			continue
		}
		members, err := matchOutboundMembers(group, *rule, view)
		if err != nil {
			return nil, err
		}
		out[group] = members
	}
	return out, nil
}
