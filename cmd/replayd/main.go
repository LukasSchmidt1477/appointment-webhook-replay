package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/example/appointment-replay-operator/internal/infrai"
	"github.com/example/appointment-replay-operator/internal/replay"
)

type platformClient interface {
	WebhookDeliveries(context.Context, string) (json.RawMessage, error)
	RedriveDeadLetters(context.Context, string, string) (json.RawMessage, error)
}

type server struct {
	client       platformClient
	redriveEvery time.Duration
	redriveGate  chan struct{}
}

type recoveryRequest struct {
	WebhookID   string    `json:"webhook_id"`
	Queue       string    `json:"queue"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	OperationID string    `json:"operation_id"`
}

type recoveryResponse struct {
	Actions        []replay.Action `json:"actions"`
	RedriveStarted bool            `json:"redrive_started"`
}

func main() {
	baseURL := os.Getenv("INFRAI_BASE_URL")
	if baseURL == "" {
		baseURL = infrai.DefaultBaseURL
	}
	client, err := infrai.NewClient(baseURL, os.Getenv("INFRAI_API_KEY"), nil)
	if err != nil {
		slog.Error("configuration rejected", "error", err)
		os.Exit(1)
	}
	rate, err := strconv.Atoi(envOr("REDRIVE_PER_MINUTE", "6"))
	if err != nil || rate < 1 {
		slog.Error("REDRIVE_PER_MINUTE must be a positive integer")
		os.Exit(1)
	}
	s := &server{client: client, redriveEvery: time.Minute / time.Duration(rate), redriveGate: make(chan struct{}, 1)}
	s.redriveGate <- struct{}{}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /recover", s.recover)
	address := envOr("LISTEN_ADDR", ":8080")
	slog.Info("appointment replay service listening", "address", address, "redrive_per_minute", rate)
	if err := http.ListenAndServe(address, mux); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func (s *server) recover(w http.ResponseWriter, r *http.Request) {
	var input recoveryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if input.WebhookID == "" || input.Queue == "" || input.OperationID == "" || !input.WindowStart.Before(input.WindowEnd) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "webhook_id, queue, operation_id, and an ordered window are required"})
		return
	}

	raw, err := s.client.WebhookDeliveries(r.Context(), input.WebhookID)
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	var deliveryResponse struct {
		Items []replay.Delivery `json:"items"`
	}
	if err := json.Unmarshal(raw, &deliveryResponse); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "unexpected delivery history"})
		return
	}
	actions, err := replay.Plan(deliveryResponse.Items, input.WindowStart, input.WindowEnd)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid appointment event"})
		return
	}

	select {
	case <-s.redriveGate:
		defer time.AfterFunc(s.redriveEvery, func() { s.redriveGate <- struct{}{} })
	case <-r.Context().Done():
		writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "request cancelled"})
		return
	}
	if _, err := s.client.RedriveDeadLetters(r.Context(), input.Queue, input.OperationID); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	for _, action := range actions {
		slog.Info("appointment reconciliation planned", "appointment_id", action.AppointmentID, "state", action.State, "delivery_id", action.DeliveryID)
	}
	writeJSON(w, http.StatusOK, recoveryResponse{Actions: actions, RedriveStarted: true})
}

func (s *server) writeUpstreamError(w http.ResponseWriter, err error) {
	var apiErr *infrai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		writeJSON(w, apiErr.HTTPStatus, map[string]string{"error": apiErr.Code, "message": apiErr.Message})
		return
	}
	slog.Error("platform request failed", "error", err)
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "platform request failed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
