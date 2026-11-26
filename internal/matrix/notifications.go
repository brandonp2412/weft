package matrix

import (
	"context"
	"encoding/json"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/pushrules"
)

// Apply account data before delivering any alerts from the same sync response.
func (b *Backend) updateAlertSettings(res *mautrix.RespSync, full bool) {
	if full || b.lowPriority == nil {
		b.lowPriority = make(map[id.RoomID]bool)
	}
	if full {
		b.alertRules = nil
		b.alertMemberCounts = make(map[id.RoomID]int)
		b.alertSettingsUnavailable = false
	}
	if b.alertMemberCounts == nil {
		b.alertMemberCounts = make(map[id.RoomID]int)
	}
	for room, data := range res.Rooms.Join {
		if data.Summary.JoinedMemberCount != nil {
			b.alertMemberCounts[room] = *data.Summary.JoinedMemberCount
		} else {
			// Membership changes without a new summary invalidate the cached count.
			for _, evt := range append(append([]*event.Event(nil), data.State.Events...), data.Timeline.Events...) {
				if evt.Type.Type == event.StateMember.Type {
					delete(b.alertMemberCounts, room)
					break
				}
			}
		}
		for _, evt := range data.AccountData.Events {
			if evt.Type.Type != event.AccountDataRoomTags.Type {
				continue
			}
			var tags event.TagEventContent
			if json.Unmarshal(evt.Content.VeryRaw, &tags) == nil {
				if _, low := tags.Tags[event.RoomTagLowPriority]; low {
					b.lowPriority[room] = true
				} else {
					delete(b.lowPriority, room)
				}
			}
		}
	}
	for room := range res.Rooms.Leave {
		delete(b.lowPriority, room)
		delete(b.alertMemberCounts, room)
	}
	for _, evt := range res.AccountData.Events {
		if evt.Type.Type != "m.push_rules" {
			continue
		}
		var rules struct {
			Global pushrules.PushRuleset `json:"global"`
		}
		if json.Unmarshal(evt.Content.VeryRaw, &rules) == nil {
			b.alertRules = &rules.Global
			b.alertSettingsUnavailable = false
		}
	}
}

// Evaluate the original wire event, before decryption can discard bridge metadata.
// Account-wide push rules apply to desktop alerts too, in Matrix priority order.
func (b *Backend) eventAllowsAlert(ctx context.Context, evt *event.Event) bool {
	if b.alertRules == nil {
		return true // No account rules supplied (e.g. a test or unconfigured server).
	}
	room := &alertRoom{backend: b, ctx: ctx, room: evt.RoomID}
	rule := b.alertRules.GetMatchingRule(room, evt)
	if room.failed || rule == nil {
		return false
	}
	for _, action := range rule.Actions {
		if action != nil && (action.Action == pushrules.ActionNotify || action.Action == pushrules.ActionCoalesce) {
			return true
		}
	}
	return false
}

type alertRoom struct {
	backend *Backend
	ctx     context.Context
	room    id.RoomID
	failed  bool
}

func (r *alertRoom) GetOwnDisplayname() string {
	return r.backend.Store.MemberName(r.room.String(), r.backend.Client.UserID.String())
}

func (r *alertRoom) GetMemberCount() int {
	if count, ok := r.backend.alertMemberCounts[r.room]; ok {
		return count
	}
	if r.failed {
		return 0
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	members, err := r.backend.Client.JoinedMembers(ctx, r.room)
	if err != nil {
		r.failed = true
		return 0
	}
	count := len(members.Joined)
	r.backend.alertMemberCounts[r.room] = count
	return count
}
