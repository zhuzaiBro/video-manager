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

type update struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		ID   int64  `json:"message_id"`
		Text string `json:"text"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Reply *struct {
			ID int64 `json:"message_id"`
		} `json:"reply_to_message"`
	} `json:"message"`
}

func (s *Service) Webhook(ctx context.Context, secret string, raw []byte) error {
	b, err := s.bot(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWebhook
	}
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(s.mac(fmt.Sprintf("telegram/%d", b.ID))), []byte(secret)) {
		return ErrWebhook
	}
	var u update
	if json.Unmarshal(raw, &u) != nil || u.Message == nil || u.ID <= 0 {
		return nil
	}
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
	var session string
	err = tx.QueryRowContext(ctx, `INSERT INTO public.messages(session_id,sender_type,content)
	   SELECT m.session_id,'admin',$5 FROM support.telegram_deliveries d JOIN public.messages m ON m.id=d.site_id
	   JOIN support.telegram_bindings a ON a.bot_id=d.bot_id AND a.chat_id=d.chat_id
	   WHERE d.bot_id=$1 AND d.chat_id=$2 AND d.tg_message_id=$3 AND a.user_id=$4 RETURNING session_id`,
		b.ID, m.Chat.ID, m.Reply.ID, m.From.ID, text).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
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
	var id, session, content string
	err = tx.QueryRowContext(ctx, `SELECT a.chat_id,m.id,m.session_id,m.content
	  FROM support.telegram_bindings a JOIN public.messages m ON m.created_at>=a.bound_at AND m.sender_type='user'
	  LEFT JOIN support.telegram_deliveries d ON d.bot_id=a.bot_id AND d.chat_id=a.chat_id AND d.site_id=m.id
	  WHERE a.bot_id=$1 AND EXISTS(SELECT 1 FROM support.telegram_bot t WHERE t.bot_id=a.bot_id)
	  AND d.tg_message_id IS NULL AND (d.retry_at IS NULL OR d.retry_at<=now())
	  ORDER BY m.created_at,m.id,a.chat_id LIMIT 1`, b.ID).Scan(&chat, &id, &session, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var result struct {
		ID int64 `json:"message_id"`
	}
	sendErr := s.tg(ctx, b.Token, "sendMessage", map[string]any{"chat_id": chat, "text": "网站访客 · " + session[:8] + "\n\n" + content + "\n\n直接回复这条消息即可。"}, &result)
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
