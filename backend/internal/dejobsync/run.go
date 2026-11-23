package dejobsync

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

//go:embed upsert.sql
var upsert string

func Run(ctx context.Context, db *sql.DB, token string) (runErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return errors.New("could not connect to the DeJob database")
	}
	defer conn.Close()
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtext('zood.job-sync.dejob'))").Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		log.Print("DeJob sync already running")
		return nil
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(c, "SELECT pg_advisory_unlock(hashtext('zood.job-sync.dejob'))")
	}()

	var source, runID string
	var checkpoint int64
	var retryAt sql.NullTime
	err = conn.QueryRowContext(ctx, `SELECT id, coalesce((sync_config->>'latest_created_ms')::bigint,0), (sync_config->>'retry_at')::timestamptz
		FROM public.job_sources WHERE slug='dejob' AND is_active`).Scan(&source, &checkpoint, &retryAt)
	if errors.Is(err, sql.ErrNoRows) {
		log.Print("DeJob source is inactive")
		return nil
	}
	if err != nil {
		return err
	}
	if retryAt.Valid && time.Now().Before(retryAt.Time) {
		log.Printf("DeJob rate limit: next eligible after %s", retryAt.Time.UTC().Format(time.RFC3339))
		return nil
	}
	// A terminated process releases its lock. Close its unfinished run before starting ours.
	if _, err = conn.ExecContext(ctx, `UPDATE public.job_sync_runs SET status='failed', finished_at=now(), error_message='Previous sync interrupted'
		WHERE source_id=$1 AND status='running'`, source); err != nil {
		return err
	}
	if err = conn.QueryRowContext(ctx, `INSERT INTO public.job_sync_runs (source_id,status) VALUES ($1,'running') RETURNING id`, source).Scan(&runID); err != nil {
		return err
	}
	fetched := 0
	defer func() {
		if runErr == nil {
			return
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(c, `UPDATE public.job_sync_runs SET status='failed',jobs_fetched=$2,error_message=$3,finished_at=now() WHERE id=$1 AND status='running'`, runID, fetched, runErr.Error()); err != nil {
			log.Print("Could not record failed DeJob run")
		}
	}()

	rows, newest, err := newFeed(token).latest(ctx, checkpoint)
	if err != nil {
		var limit *rateLimited
		if errors.As(err, &limit) {
			if _, setErr := conn.ExecContext(ctx, `UPDATE public.job_sources SET sync_config=jsonb_set(sync_config,'{retry_at}',to_jsonb($2::timestamptz)) WHERE id=$1`, source, limit.retryAt); setErr != nil {
				return fmt.Errorf("save DeJob retry time: %w", setErr)
			}
		}
		return err
	}
	fetched = len(rows)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var created, updated int
	if fetched > 0 {
		payload, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, upsert, source, string(payload)).Scan(&created, &updated); err != nil {
			return fmt.Errorf("save DeJob listings: %w", err)
		}
	}
	if newest > checkpoint || retryAt.Valid {
		if _, err = tx.ExecContext(ctx, `UPDATE public.job_sources SET sync_config=jsonb_set(sync_config - 'retry_at','{latest_created_ms}',to_jsonb($2::bigint)) WHERE id=$1`, source, newest); err != nil {
			return err
		}
	}
	skipped := fetched - created - updated
	if _, err = tx.ExecContext(ctx, `UPDATE public.job_sync_runs SET status='success',jobs_fetched=$2,
		jobs_created=$3,jobs_skipped=$4,finished_at=now() WHERE id=$1`, runID, fetched, created, skipped); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	log.Printf("DeJob sync complete: fetched=%d created=%d updated=%d unchanged=%d", fetched, created, updated, skipped)
	return nil
}
