package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Broadcast posts one private Realtime event through the documented REST
// endpoint. The topic is the client channel name. A 2xx response only means
// the HTTP API accepted the request; it does not prove a browser received it.
type Broadcast struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewBroadcast(baseURL, apiKey string) *Broadcast {
	return &Broadcast{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (b *Broadcast) Publish(ctx context.Context, topic string, payload map[string]any) error {
	if b == nil || b.baseURL == "" || b.apiKey == "" {
		return NewError(http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "服务端还没有配置实时频道")
	}
	if !strings.HasPrefix(topic, supportTopicPrefix) || strings.ContainsAny(topic, "/?#") {
		return NewError(http.StatusBadRequest, "INVALID_INPUT", "实时频道不合法")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return NewError(http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "实时消息无法发送")
	}
	endpoint := b.baseURL + "/realtime/v1/api/broadcast/" + url.PathEscape(topic) + "/events/message?private=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return NewError(http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "实时消息无法发送")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", b.apiKey)
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	resp, err := b.http.Do(req)
	if err != nil {
		return NewError(http.StatusBadGateway, "REALTIME_UNAVAILABLE", "实时消息发送失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("realtime broadcast status %d", resp.StatusCode)
	}
	return nil
}
