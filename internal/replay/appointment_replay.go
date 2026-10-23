package replay

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type Delivery struct {
	DeliveryID string          `json:"delivery_id"`
	Payload    json.RawMessage `json:"payload"`
}

type AppointmentEvent struct {
	AppointmentID string    `json:"appointment_id"`
	Event         string    `json:"event"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type Action struct {
	DeliveryID    string `json:"delivery_id"`
	AppointmentID string `json:"appointment_id"`
	State         string `json:"state"`
	Notification  string `json:"notification"`
}

var stateByEvent = map[string]string{
	"appointment.created":     "scheduled",
	"appointment.rescheduled": "rescheduled",
	"appointment.cancelled":   "cancelled",
	"appointment.completed":   "completed",
}

func Plan(deliveries []Delivery, start, end time.Time) ([]Action, error) {
	type candidate struct {
		delivery Delivery
		event    AppointmentEvent
	}
	latest := make(map[string]candidate)
	for _, delivery := range deliveries {
		var event AppointmentEvent
		if err := json.Unmarshal(delivery.Payload, &event); err != nil {
			return nil, fmt.Errorf("decode delivery %q: %w", delivery.DeliveryID, err)
		}
		state, known := stateByEvent[event.Event]
		if !known || event.AppointmentID == "" || event.OccurredAt.Before(start) || !event.OccurredAt.Before(end) {
			continue
		}
		previous, seen := latest[event.AppointmentID]
		if !seen || event.OccurredAt.After(previous.event.OccurredAt) {
			latest[event.AppointmentID] = candidate{delivery: delivery, event: event}
		}
		_ = state
	}

	actions := make([]Action, 0, len(latest))
	for _, item := range latest {
		state := stateByEvent[item.event.Event]
		actions = append(actions, Action{
			DeliveryID:    item.delivery.DeliveryID,
			AppointmentID: item.event.AppointmentID,
			State:         state,
			Notification:  fmt.Sprintf("appointment %s requires %s reconciliation", item.event.AppointmentID, state),
		})
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].AppointmentID < actions[j].AppointmentID })
	return actions, nil
}
