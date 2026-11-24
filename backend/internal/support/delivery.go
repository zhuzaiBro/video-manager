package support

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

var ErrWebhook = errors.New("unverified webhook")

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type telegramUser struct {
	ID int64 `json:"id"`
}
type callback struct {
	ID      string       `json:"id"`
	Data    string       `json:"data"`
	From    telegramUser `json:"from"`
	Message struct {
		ID   int64        `json:"message_id"`
		Chat telegramChat `json:"chat"`
	} `json:"message"`
}

type update struct {
	Callback *callback `json:"callback_query"`
	ID       int64     `json:"update_id"`
	Message  *struct {
		ID    int64        `json:"message_id"`
		Text  string       `json:"text"`
		Chat  telegramChat `json:"chat"`
		From  telegramUser `json:"from"`
		Reply *struct {
			ID int64 `json:"message_id"`
		} `json:"reply_to_message"`
	} `json:"message"`
}

// A callback is answered in the webhook HTTP response, as supported by the Telegram Bot API.
func (s *Service) Webhook(ctx context.Context, secret string, raw []byte) (map[string]any, error) {
	b, err := s.bot(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhook
	}
	if err != nil {
		return nil, err
	}
	if !hmac.Equal([]byte(s.mac(fmt.Sprintf("telegram/%d", b.ID))), []byte(secret)) {
		return nil, ErrWebhook
	}
	var u update
	if json.Unmarshal(raw, &u) != nil || u.ID <= 0 {
		return nil, nil
	}
	if q := u.Callback; q != nil {
		if q.Data != "support_read" || q.ID == "" || q.Message.Chat.Type != "private" || q.From.ID != q.Message.Chat.ID {
			return nil, nil
		}
		answer := map[string]any{"method": "answerCallbackQuery", "callback_query_id": q.ID}
		session, target, err := deliveryTarget(ctx, s.db, b.ID, q.From.ID, q.Message.ID)
		if errors.Is(err, sql.ErrNoRows) {
			answer["text"] = "这条消息没有关联当前客服会话"
			return answer, nil
		}
		if err != nil {
			return nil, err
		}
		if err = s.MarkRead(ctx, session, "user", []string{target}); err != nil {
			return nil, err
		}
		answer["text"] = "已标为已读"
		return answer, nil
	}
	if u.Message == nil {
		return nil, nil
	}
	return nil, s.handleMessage(ctx, b, u)
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func deliveryTarget(ctx context.Context, db rowQuerier, botID, chatID, messageID int64) (string, string, error) {
	var session, target string
	err := db.QueryRowContext(ctx, `SELECT m.session_id,m.id FROM support.telegram_deliveries d
		JOIN public.messages m ON m.id=d.site_id AND m.sender_type='user'
		JOIN support.telegram_bindings a ON a.bot_id=d.bot_id AND a.chat_id=d.chat_id AND a.user_id=d.chat_id
		JOIN support.telegram_bot b ON b.bot_id=d.bot_id AND b.singleton
		WHERE d.bot_id=$1 AND d.chat_id=$2 AND d.tg_message_id=$3`, botID, chatID, messageID).Scan(&session, &target)
	return session, target, err
}
func (s *Service) handleMessage(ctx context.Context, b bot, u update) error {
	m := u.Message
	if m.Chat.Type != "private" || m.From.ID != m.Chat.ID {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "INSERT INTO support.telegram_updates(bot_id,update_id) VALUES($1,$2) ON CONFLICT DO NOTHING", b.ID, u.ID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return err
	}
	text := strings.TrimSpace(m.Text)
	parts := strings.Fields(text)
	if len(parts) == 2 && parts[0] == "/start" {
		hash := sha256.Sum256([]byte(parts[1]))
		result, err := tx.ExecContext(ctx, `WITH valid AS (UPDATE support.telegram_bot SET bind_hash=NULL,bind_expires=NULL
		   WHERE singleton AND bot_id=$1 AND bind_hash=$2 AND bind_expires>now() RETURNING bot_id)
		   INSERT INTO support.telegram_bindings(bot_id,chat_id,user_id)
		   SELECT bot_id,$3,$4 FROM valid ON CONFLICT(bot_id,chat_id) DO UPDATE SET user_id=excluded.user_id`, b.ID, hash[:], m.Chat.ID, m.From.ID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if count > 0 {
			_ = s.tg(ctx, b.Token, "sendMessage", map[string]any{"chat_id": m.Chat.ID, "text": "客服已绑定。直接回复访客消息，即可把回复发回网站。"}, nil)
			s.Notify("inbox")
		}
		return nil
	}
	if m.Reply == nil || text == "" {
		return tx.Commit()
	}
	runes := []rune(text)
	if len(runes) > 2000 {
		text = string(runes[:2000])
	}
	session, target, err := deliveryTarget(ctx, tx, b.ID, m.Chat.ID, m.Reply.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err = recordRead(ctx, tx, session, "user", []string{target}); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.messages(session_id,sender_type,content) VALUES($1,'admin',$2)`, session, text); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		s.Notify(session)
	}
	return err
}

// The inbox is durable. Pending deliveries are selected from canonical messages
// newer than each binding; an accepted reply always maps back to that message.
func (s *Service) Deliver(ctx context.Context) (bool, error) {
	b, err := s.bot(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(hashtext('support.telegram.deliver'))").Scan(&locked); err != nil || !locked {
		return false, err
	}
	var chat int64
	var id, session, content, displayName, username string
	var member bool
	err = tx.QueryRowContext(ctx, `SELECT a.chat_id,m.id,m.session_id,m.content,m.user_id IS NOT NULL,
	  COALESCE(p.display_name,''),COALESCE(p.username,'')
	  FROM support.telegram_bindings a JOIN public.messages m ON m.created_at>=a.bound_at AND m.sender_type='user'
	  LEFT JOIN public.user_profiles p ON p.id=m.user_id
	  LEFT JOIN support.telegram_deliveries d ON d.bot_id=a.bot_id AND d.chat_id=a.chat_id AND d.site_id=m.id
	  WHERE a.bot_id=$1 AND EXISTS(SELECT 1 FROM support.telegram_bot t WHERE t.bot_id=a.bot_id)
	  AND d.tg_message_id IS NULL AND (d.retry_at IS NULL OR d.retry_at<=now())
	  ORDER BY m.created_at,m.id,a.chat_id LIMIT 1`, b.ID).Scan(&chat, &id, &session, &content, &member, &displayName, &username)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var result struct {
		ID int64 `json:"message_id"`
	}
	sender := CustomerName(session, member, displayName, username)
	sendErr := s.tg(ctx, b.Token, "sendMessage", map[string]any{
		"chat_id": chat, "text": sender + "\n\n" + content + "\n\n直接回复这条消息即可。",
		"reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "标为已读", "callback_data": "support_read"}}}},
	}, &result)
	if sendErr != nil || result.ID == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO support.telegram_deliveries(bot_id,chat_id,site_id,attempts,retry_at)
		  VALUES($1,$2,$3,1,now()+interval '1 minute') ON CONFLICT(bot_id,chat_id,site_id) DO UPDATE SET
		  attempts=support.telegram_deliveries.attempts+1,retry_at=now()+least(support.telegram_deliveries.attempts+1,30)*interval '1 minute'`, b.ID, chat, id)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO support.telegram_deliveries(bot_id,chat_id,site_id,tg_message_id) VALUES($1,$2,$3,$4)
		  ON CONFLICT(bot_id,chat_id,site_id) DO UPDATE SET tg_message_id=excluded.tg_message_id,retry_at=NULL`, b.ID, chat, id, result.ID)
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Service) Run(ctx context.Context) {
	defer s.db.Close()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var nextRegistration time.Time
	registered := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !registered && !time.Now().Before(nextRegistration) {
				err := s.refreshSavedWebhook(ctx)
				registered = err == nil || errors.Is(err, sql.ErrNoRows)
				nextRegistration = time.Now().Add(5 * time.Minute)
				if err == nil {
					log.Print("Support Telegram read callbacks enabled")
				}
			}
			for count := 0; count < 15; count++ {
				worked, err := s.Deliver(ctx)
				if err != nil {
					log.Print("Support delivery queue unavailable")
					break
				}
				if !worked {
					break
				}
			}
		}
	}
}
