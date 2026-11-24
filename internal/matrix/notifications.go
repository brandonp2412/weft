package matrix

import (
	"encoding/json"

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
		b.mutedRooms = nil
		b.alertSettingsUnavailable = false
	}
	for room, data := range res.Rooms.Join {
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
	}
	for _, evt := range res.AccountData.Events {
		if evt.Type.Type != "m.push_rules" {
			continue
		}
		var rules struct {
			Global pushrules.PushRuleset `json:"global"`
		}
		if json.Unmarshal(evt.Content.VeryRaw, &rules) == nil {
			b.mutedRooms = mutedRooms(&rules.Global)
		}
	}
}

func mutedRooms(rules *pushrules.PushRuleset) map[id.RoomID]bool {
	rooms := make(map[id.RoomID]bool)
	for room, rule := range rules.Room.Map {
		if suppressesAlerts(rule) {
			rooms[id.RoomID(room)] = true
		}
	}
	for _, rule := range rules.Override {
		if !suppressesAlerts(rule) || len(rule.Conditions) != 1 {
			continue
		}
		condition := rule.Conditions[0]
		if condition == nil || condition.Key != "room_id" {
			continue
		}
		if condition.Kind == pushrules.KindEventMatch && condition.Pattern != "" {
			rooms[id.RoomID(condition.Pattern)] = true
		}
		if condition.Kind == pushrules.KindEventPropertyIs {
			if room, ok := condition.Value.(string); ok {
				rooms[id.RoomID(room)] = true
			}
		}
	}
	return rooms
}

func suppressesAlerts(rule *pushrules.PushRule) bool {
	if rule == nil || !rule.Enabled {
		return false
	}
	for _, action := range rule.Actions {
		if action != nil && (action.Action == pushrules.ActionNotify || action.Action == pushrules.ActionCoalesce) {
			return false
		}
	}
	return true
}
