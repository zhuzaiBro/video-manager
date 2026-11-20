package supabase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zood/video-manager/internal/platform"
)

type Client struct {
	baseURL string
	anonKey string
	http    *http.Client
}

func New(baseURL, anonKey string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	anonKey = strings.TrimSpace(anonKey)
	if baseURL == "" || anonKey == "" {
		return nil, fmt.Errorf("SUPABASE_URL and SUPABASE_ANON_KEY are required")
	}
	if err := rejectPrivilegedKey(anonKey); err != nil {
		return nil, err
	}
	return &Client{
		baseURL: baseURL,
		anonKey: anonKey,
		http:    &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func rejectPrivilegedKey(key string) error {
	if strings.HasPrefix(key, "sb_secret_") || strings.Contains(strings.ToLower(key), "service_role") {
		return fmt.Errorf("refusing a privileged Supabase key")
	}
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	role, _ := claims["role"].(string)
	if role == "service_role" {
		return fmt.Errorf("refusing a service_role key")
	}
	return nil
}

func (c *Client) Select(ctx context.Context, caller platform.Caller, query platform.Query) (platform.Result, error) {
	params, err := platform.SupabaseParams(query)
	if err != nil {
		return platform.Result{}, err
	}
	status, body, count, err := c.do(ctx, caller, http.MethodGet, "/rest/v1/"+query.Table+"?"+params.Encode(), nil, query.Count)
	if err != nil {
		return platform.Result{}, err
	}
	rows, err := decodeRows(status, body)
	if err != nil {
		return platform.Result{}, err
	}
	return platform.Result{Rows: rows, Count: count}, nil
}

func (c *Client) Write(ctx context.Context, caller platform.Caller, method string, query platform.Query, payload any) (platform.Result, error) {
	params, err := platform.SupabaseParams(query)
	if err != nil {
		return platform.Result{}, err
	}
	status, body, _, err := c.do(ctx, caller, method, "/rest/v1/"+query.Table+"?"+params.Encode(), payload, false)
	if err != nil {
		return platform.Result{}, err
	}
	rows, err := decodeRows(status, body)
	if err != nil {
		return platform.Result{}, err
	}
	if (method == http.MethodPatch || method == http.MethodDelete) && len(rows) == 0 {
		return platform.Result{}, platform.NewError(http.StatusNotFound, "NOT_FOUND", "记录不存在或无权访问")
	}
	return platform.Result{Rows: rows}, nil
}

func (c *Client) FetchSelf(ctx context.Context, caller platform.Caller) (platform.AuthAccount, error) {
	if !caller.SignedIn() {
		return platform.AuthAccount{}, platform.NewError(http.StatusUnauthorized, "UNAUTHORIZED", "未登录")
	}
	status, body, _, err := c.do(ctx, caller, http.MethodGet, "/auth/v1/user", nil, false)
	if err != nil {
		return platform.AuthAccount{}, err
	}
	if status >= 400 {
		return platform.AuthAccount{}, upstreamError(status, body)
	}
	return decodeAuthAccount(body)
}

func decodeAuthAccount(body []byte) (platform.AuthAccount, error) {
	var payload struct {
		ID        string         `json:"id"`
		Email     string         `json:"email"`
		Phone     string         `json:"phone"`
		CreatedAt string         `json:"created_at"`
		Metadata  map[string]any `json:"user_metadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return platform.AuthAccount{}, err
	}
	created, _ := time.Parse(time.RFC3339, payload.CreatedAt)
	return platform.AuthAccount{ID: payload.ID, Email: payload.Email, Phone: payload.Phone, CreatedAt: created, Metadata: payload.Metadata}, nil
}

func (c *Client) Upload(ctx context.Context, caller platform.Caller, bucket, path, contentType string, body []byte) error {
	if !caller.SignedIn() {
		return platform.NewError(http.StatusUnauthorized, "UNAUTHORIZED", "未登录")
	}
	status, respBody, _, err := c.doRaw(ctx, caller, http.MethodPost, "/storage/v1/object/"+bucket+"/"+path, body, contentType, false)
	if err != nil {
		return err
	}
	if status >= 400 {
		return storageError(status, respBody)
	}
	return nil
}

func (c *Client) SignedURL(ctx context.Context, caller platform.Caller, bucket, objectPath string, expiresIn int) (string, error) {
	if !caller.SignedIn() {
		return "", platform.NewError(http.StatusUnauthorized, "UNAUTHORIZED", "未登录")
	}
	if expiresIn <= 0 || expiresIn > 3600 {
		expiresIn = 600
	}
	status, body, _, err := c.do(ctx, caller, http.MethodPost, "/storage/v1/object/sign/"+bucket+"/"+objectPath, map[string]any{"expiresIn": expiresIn}, false)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", storageError(status, body)
	}
	var payload struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.SignedURL == "" {
		return "", platform.NewError(http.StatusBadGateway, "STORAGE", "没有拿到签名地址")
	}
	if strings.HasPrefix(payload.SignedURL, "http") {
		return payload.SignedURL, nil
	}
	return c.baseURL + "/storage/v1" + payload.SignedURL, nil
}

func (c *Client) PublicURL(bucket, path string) string {
	return c.baseURL + "/storage/v1/object/public/" + bucket + "/" + path
}

func (c *Client) do(ctx context.Context, caller platform.Caller, method, path string, payload any, count bool) (int, []byte, *int, error) {
	var body []byte
	contentType := "application/json"
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, nil, err
		}
		body = encoded
	}
	return c.doRaw(ctx, caller, method, path, body, contentType, count)
}

func (c *Client) doRaw(ctx context.Context, caller platform.Caller, method, path string, body []byte, contentType string, count bool) (int, []byte, *int, error) {
	var reader io.Reader
	if body != nil && method != http.MethodGet {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("apikey", c.anonKey)
	req.Header.Set("Accept", "application/json")
	if caller.SignedIn() {
		req.Header.Set("Authorization", "Bearer "+caller.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.anonKey)
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Content-Type", contentType)
		prefer := "return=representation"
		if strings.Contains(path, "on_conflict=") {
			prefer += ",resolution=merge-duplicates"
		}
		req.Header.Set("Prefer", prefer)
	}
	if count {
		req.Header.Set("Prefer", "count=exact")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, platform.NewError(http.StatusBadGateway, "UPSTREAM", "数据服务暂时不可用")
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, nil, err
	}
	var total *int
	if contentRange := resp.Header.Get("Content-Range"); contentRange != "" {
		if parsed, ok := parseCount(contentRange); ok {
			total = &parsed
		}
	}
	return resp.StatusCode, respBody, total, nil
}

func parseCount(contentRange string) (int, bool) {
	parts := strings.Split(contentRange, "/")
	if len(parts) != 2 || parts[1] == "*" {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(parts[1], "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

func decodeRows(status int, body []byte) ([]map[string]any, error) {
	if status >= 400 {
		return nil, upstreamError(status, body)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return []map[string]any{}, nil
	}
	if body[0] == '{' {
		var row map[string]any
		if err := json.Unmarshal(body, &row); err != nil {
			return nil, err
		}
		if _, isErr := row["message"]; isErr && row["code"] != nil {
			return nil, upstreamError(status, body)
		}
		return []map[string]any{row}, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func upstreamError(status int, body []byte) error {
	var payload struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(body, &payload)
	message := payload.Message
	if message == "" {
		message = "数据服务拒绝了请求"
	}
	code := payload.Code
	if code == "" {
		code = "UPSTREAM"
	}
	if status == http.StatusUnauthorized {
		return platform.NewError(http.StatusUnauthorized, "UNAUTHORIZED", message)
	}
	if status == http.StatusForbidden || code == "42501" {
		return platform.NewError(http.StatusForbidden, "FORBIDDEN", message)
	}
	if status >= 500 {
		return platform.NewError(http.StatusBadGateway, code, message)
	}
	return platform.NewError(status, code, message)
}

func storageError(status int, body []byte) error {
	err := upstreamError(status, body)
	if apiErr, ok := err.(*platform.Error); ok {
		message := strings.ToLower(apiErr.Message)
		if strings.Contains(message, "bucket") && strings.Contains(message, "not found") {
			apiErr.Message = "附件存储未初始化"
		}
		if strings.Contains(message, "row-level security") || strings.Contains(message, "permission") || strings.Contains(message, "unauthorized") {
			apiErr.Status = http.StatusForbidden
			apiErr.Code = "FORBIDDEN"
		}
	}
	return err
}
