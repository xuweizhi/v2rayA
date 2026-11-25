package service

import (
	"errors"
	"fmt"
	"slices"

	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

func DeleteWhich(ws []*configure.Which) (err error) {
	membershipMu.Lock()
	defer membershipMu.Unlock()
	// The API uses 1-based IDs, while both database lists use 0-based sort
	// indices. Group, latency and link metadata do not identify a list row.
	serverCount, err := db.ListLen("touch", "servers")
	if err != nil {
		return err
	}
	subCount, err := db.ListLen("touch", "subscriptions")
	if err != nil {
		return err
	}
	var serverIndexes, subscriptionIndexes []int
	for _, w := range ws {
		if w == nil {
			continue
		}
		switch w.TYPE {
		case configure.ServerType:
			if w.ID < 1 || w.ID > serverCount {
				return fmt.Errorf("invalid server index %d", w.ID)
			}
			serverIndexes = append(serverIndexes, w.ID-1)
		case configure.SubscriptionType:
			if w.ID < 1 || w.ID > subCount {
				return fmt.Errorf("invalid subscription index %d", w.ID)
			}
			subscriptionIndexes = append(subscriptionIndexes, w.ID-1)
		}
	}
	if len(subscriptionIndexes)+len(serverIndexes) == 0 {
		return nil
	}
	slices.Sort(serverIndexes)
	serverIndexes = slices.Compact(serverIndexes)
	slices.Sort(subscriptionIndexes)
	subscriptionIndexes = slices.Compact(subscriptionIndexes)
	deletedSubIDs := make(map[int64]bool)
	for _, index := range subscriptionIndexes {
		var id int64
		if err := db.GetDB().QueryRow("SELECT id FROM subscriptions WHERE sort = ?", index).Scan(&id); err != nil {
			return err
		}
		deletedSubIDs[id] = true
	}

	groups := configure.GetOutbounds()
	nextStates := make(map[string]any)
	oldStates := make(map[string]any)
	runtimeStates := make(map[string]any)
	disconnected := false
	for _, group := range groups {
		previous := configure.GetConnectedServersByOutbound(group)
		beforeDelete, afterDelete := configure.NewWhiches(nil), configure.NewWhiches(nil)
		for _, w := range previous.Get() {
			cp := *w
			var earlier int
			var removed bool
			switch w.TYPE {
			case configure.ServerType:
				earlier, removed = slices.BinarySearch(serverIndexes, w.ID-1)
				cp.ID -= earlier
			case configure.SubscriptionServerType:
				earlier, removed = slices.BinarySearch(subscriptionIndexes, w.Sub)
				cp.Sub -= earlier
			}
			if removed {
				disconnected = true
				continue
			}
			beforeDelete.Add(*w)
			afterDelete.Add(cp)
		}
		key := memberKey(group)
		if beforeDelete.Len() != previous.Len() {
			runtimeStates[key] = beforeDelete
			oldStates[key] = previous
		}
		if !sameMembers(previous.Get(), afterDelete.Get()) {
			nextStates[key] = afterDelete
		}
	}

	// Prune deleted subscription IDs from every automatic outbound group's rule
	// so remaining rules never fail with "source subscription #N no longer exists".
	if len(deletedSubIDs) > 0 {
		for _, group := range groups {
			setting := configure.GetOutboundSetting(group)
			if setting.MemberRule == nil || len(setting.MemberRule.SubscriptionIDs) == 0 {
				continue
			}
			previousSetting := setting
			memberRule := *setting.MemberRule
			setting.MemberRule = &memberRule
			remaining := make([]int64, 0, len(setting.MemberRule.SubscriptionIDs))
			pruned := false
			for _, id := range setting.MemberRule.SubscriptionIDs {
				if deletedSubIDs[id] {
					pruned = true
					continue
				}
				remaining = append(remaining, id)
			}
			if !pruned {
				continue
			}
			if len(remaining) == 0 {
				setting.MemberRule = nil
			} else {
				setting.MemberRule.SubscriptionIDs = remaining
			}
			key := "outbound." + group + ":setting"
			nextStates[key] = setting
			runtimeStates[key] = setting
			oldStates[key] = previousSetting
		}
	}

	// Keep all rows intact while changing a live core. A failed reload can
	// then restore the old selection, and retrying the original request is safe.
	reloaded := disconnected && v2ray.ProcessManager.Running()
	if reloaded {
		if err := applyOutboundState(runtimeStates, oldStates); err != nil {
			return err
		}
	}
	if err := db.RemoveTouchAndSetState(subscriptionIndexes, serverIndexes, nextStates); err != nil {
		if reloaded {
			if restoreErr := db.SetState(oldStates); restoreErr != nil {
				return errors.Join(err, restoreErr)
			}
			err = errors.Join(err, v2ray.UpdateV2RayConfig())
		}
		return err
	}

	AutoBackup()
	return nil
}
