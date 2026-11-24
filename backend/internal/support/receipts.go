package support

import (
	"context"
	"database/sql"
	"encoding/json"
)

type receiptWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Only already-authorized chat history IDs are supplied by the platform API.
func (s *Service) ReadState(ctx context.Context, session string, ids []string) (map[string]bool, int, error) {
	var encoded []byte
	var unread int
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT jsonb_object_agg(message_id, true) FROM support.message_reads WHERE message_id=ANY($2::uuid[])), '{}'::jsonb),
		(SELECT count(*) FROM public.messages m WHERE session_id=$1 AND sender_type='admin'
		 AND NOT EXISTS (SELECT 1 FROM support.message_reads r WHERE r.message_id=m.id))`, session, ids).Scan(&encoded, &unread)
	if err != nil {
		return nil, 0, err
	}
	var seen map[string]bool
	err = json.Unmarshal(encoded, &seen)
	return seen, unread, err
}

// Staff acknowledge individual customer messages. A customer reading down to a
// reply clears that reply and earlier replies in the same conversation.
func recordRead(ctx context.Context, db receiptWriter, session, sender string, ids []string) (bool, error) {
	query := `INSERT INTO support.message_reads(message_id,read_at)
		SELECT id,now() FROM public.messages WHERE session_id=$1 AND sender_type='user' AND id=ANY($2::uuid[])
		ORDER BY id ON CONFLICT DO NOTHING`
	if sender == "admin" {
		query = `WITH last_visible AS (
			SELECT created_at,id FROM public.messages WHERE session_id=$1 AND sender_type='admin' AND id=ANY($2::uuid[])
			ORDER BY created_at DESC,id DESC LIMIT 1
		)
		INSERT INTO support.message_reads(message_id,read_at)
		SELECT m.id,now() FROM public.messages m,last_visible v WHERE session_id=$1 AND sender_type='admin'
		AND (m.created_at,m.id)<=(v.created_at,v.id) AND NOT EXISTS (SELECT 1 FROM support.message_reads r WHERE r.message_id=m.id)
		ORDER BY m.id ON CONFLICT DO NOTHING`
	}
	res, err := db.ExecContext(ctx, query, session, ids)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Service) MarkRead(ctx context.Context, session, sender string, ids []string) error {
	changed, err := recordRead(ctx, s.db, session, sender, ids)
	if err == nil && changed {
		s.Notify(session)
	}
	return err
}
