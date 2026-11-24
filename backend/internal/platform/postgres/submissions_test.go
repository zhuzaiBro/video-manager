package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/platform"
)

func TestAdminSubmissionTagsAndCollection(t *testing.T) {
	_, dsn := startPostgresOn(t, "55489")
	db := openDB(t, dsn)
	_, err := db.Exec(`
		CREATE ROLE authenticated;
		CREATE TABLE user_profiles (id uuid PRIMARY KEY, username text, display_name text, avatar_url text, is_admin boolean);
		CREATE TABLE interview_collections (id uuid PRIMARY KEY, title text);
		CREATE TABLE interview_question_submissions (
			id uuid PRIMARY KEY, user_id uuid, collection_id uuid, title text, content text, tags text[],
			status text, admin_note text, source text, contact text, difficulty text, attachment_name text,
			attachment_path text, attachment_mime_type text, attachment_size_bytes bigint, created_at timestamptz,
			updated_at timestamptz
		);
		GRANT SELECT ON user_profiles, interview_collections, interview_question_submissions TO authenticated;
	`)
	if err != nil {
		t.Fatal(err)
	}
	adminID, collectionID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO user_profiles (id, display_name, is_admin) VALUES ($1, '测试管理员', true)`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO interview_collections VALUES ($1, '运营实战')`, collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO interview_question_submissions
		(id, user_id, collection_id, tags, source, attachment_size_bytes, created_at) VALUES
		($1, $2, $3, ARRAY['跨境运营', '问答,实战', '含"引号"'], '校招', 1234, now()),
		($4, $2, NULL, ARRAY[]::text[], NULL, NULL, now() - interval '1 day')`,
		uuid.NewString(), adminID, collectionID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	service := platform.NewService(&Source{db: db}, nil, "postgres")
	caller := platform.Caller{UserID: adminID, Token: "test-session"}
	result, err := service.ListSubmissions(context.Background(), caller)
	if err != nil || len(result.Rows) != 2 {
		t.Fatalf("list submissions: %v, rows=%d", err, len(result.Rows))
	}
	row := result.Rows[0]
	if !reflect.DeepEqual(row["tags"], []any{"跨境运营", "问答,实战", `含"引号"`}) || !reflect.DeepEqual(result.Rows[1]["tags"], []any{}) {
		t.Fatalf("tags: %#v, %#v", row["tags"], result.Rows[1]["tags"])
	}
	collection, ok := row["interview_collections"].(map[string]any)
	if !ok || collection["title"] != "运营实战" || row["source"] != "校招" || row["attachment_size_bytes"] != int64(1234) {
		t.Fatalf("submission metadata: %#v", row)
	}
	caller.UserID = uuid.NewString()
	_, err = service.ListSubmissions(context.Background(), caller)
	var denied *platform.Error
	if !errors.As(err, &denied) || denied.Status != 403 {
		t.Fatalf("unprivileged caller: %v", err)
	}
}
