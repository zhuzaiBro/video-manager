package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type telegramClient struct {
	token string
	http  *http.Client
}

func NewTelegram(token string) TelegramSender {
	return &telegramClient{token: token, http: &http.Client{Timeout: 10 * time.Second}}
}

func (t *telegramClient) Send(ctx context.Context, chatID int64, text string) (int64, error) {
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+t.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	if !payload.OK {
		return 0, NewError(http.StatusBadGateway, "TELEGRAM", "Telegram 发送失败 "+strconv.FormatInt(chatID, 10))
	}
	return payload.Result.MessageID, nil
}
