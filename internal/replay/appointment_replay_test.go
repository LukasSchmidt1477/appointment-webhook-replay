package replay

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPlanKeepsLatestSafeAppointmentState(t *testing.T) {
	windowStart := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(time.Hour)
	tests := []struct {
		name       string
		deliveries []Delivery
		want       []Action
	}{
		{
			name: "latest cancellation wins and patient data stays out of notification",
			deliveries: []Delivery{
				delivery("d-1", `{"appointment_id":"apt-42","patient_id":"patient-private","event":"appointment.rescheduled","occurred_at":"2026-09-25T01:10:00Z"}`),
				delivery("d-2", `{"appointment_id":"apt-42","patient_id":"patient-private","event":"appointment.cancelled","occurred_at":"2026-09-25T01:20:00Z"}`),
			},
			want: []Action{{DeliveryID: "d-2", AppointmentID: "apt-42", State: "cancelled", Notification: "appointment apt-42 requires cancelled reconciliation"}},
		},
		{
			name: "events outside the recovery window are ignored",
			deliveries: []Delivery{
				delivery("d-old", `{"appointment_id":"apt-7","event":"appointment.created","occurred_at":"2026-09-25T00:59:59Z"}`),
				delivery("d-unknown", `{"appointment_id":"apt-8","event":"patient.updated","occurred_at":"2026-09-25T01:30:00Z"}`),
			},
			want: []Action{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Plan(tt.deliveries, windowStart, windowEnd)
			if err != nil {
				t.Fatal(err)
			}
			if string(mustJSON(t, got)) != string(mustJSON(t, tt.want)) {
				t.Fatalf("Plan() = %s, want %s", mustJSON(t, got), mustJSON(t, tt.want))
			}
		})
	}
}

func delivery(id, payload string) Delivery {
	return Delivery{DeliveryID: id, Payload: json.RawMessage(payload)}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
