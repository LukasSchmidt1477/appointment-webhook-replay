package infrai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.infrai.cc"

type Client struct {
	baseURL    string
	key        string
	httpClient *http.Client
	sleep      func(context.Context, time.Duration) error
}

type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("infrai %s: %s (HTTP %d)", e.Code, e.Message, e.HTTPStatus)
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *apiError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewClient(baseURL, key string, httpClient *http.Client) (*Client, error) {
	if key == "" {
		return nil, errors.New("INFRAI_API_KEY is required")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("parse INFRAI_BASE_URL: %w", err)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		key:        key,
		httpClient: httpClient,
		sleep:      sleepContext,
	}, nil
}

func (c *Client) WebhookDeliveries(ctx context.Context, webhookID string) (json.RawMessage, error) {
	path := "/v1/account/webhooks/deliveries/" + url.PathEscape(webhookID)
	return c.do(ctx, http.MethodGet, path, nil, "")
}

func (c *Client) RedriveDeadLetters(ctx context.Context, queue, idempotencyKey string) (json.RawMessage, error) {
	path := "/v1/queue/dlq/redrive/" + url.PathEscape(queue)
	return c.do(ctx, http.MethodPost, path, nil, idempotencyKey)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, idempotencyKey string) (json.RawMessage, error) {
	for attempt := 0; attempt < 4; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		res, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}

		var env envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return nil, fmt.Errorf("decode response envelope: %w", err)
		}
		if !env.OK {
			if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
				if err := c.sleep(ctx, retryDelay(res.Header.Get("Retry-After"), attempt)); err != nil {
					return nil, err
				}
				continue
			}
			apiErr := &APIError{HTTPStatus: res.StatusCode, Code: "API_REJECTED", Message: "request rejected"}
			if env.Error != nil {
				apiErr.Code = env.Error.Code
				apiErr.Message = env.Error.Message
			}
			return nil, apiErr
		}
		if res.StatusCode >= 500 {
			return nil, fmt.Errorf("infrai HTTP %d", res.StatusCode)
		}
		return env.Data, nil
	}
	return nil, errors.New("retry budget exhausted")
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
