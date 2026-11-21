package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/zood/video-manager/internal/platform"
)

func (s *Source) ReadAdminStudy(ctx context.Context, query platform.AdminStudyQuery, yesterdayStart, todayStart, todayEnd time.Time) (platform.AdminStudyData, error) {
	caller, err := callerFrom(ctx)
	if err != nil {
		return platform.AdminStudyData{}, err
	}
	var data platform.AdminStudyData
	err = s.withScope(ctx, caller, func(tx *sql.Tx) error {
		like := ""
		if query.Keyword != "" {
			like = "%" + escapeLike(query.Keyword) + "%"
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM public.user_profiles),
				(SELECT COUNT(*) FROM public.user_profiles AS profiles
					WHERE $1 = ''
						OR profiles.username ILIKE $2 ESCAPE '\'
						OR COALESCE(profiles.display_name, '') ILIKE $2 ESCAPE '\'
						OR profiles.id::text ILIKE $2 ESCAPE '\')
		`, query.Keyword, like).Scan(&data.UserCount, &data.FilteredTotal); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT
				COUNT(DISTINCT user_id) FILTER (WHERE created_at >= $2 AND created_at < $3),
				COUNT(DISTINCT user_id) FILTER (WHERE created_at >= $1 AND created_at < $2),
				COALESCE(SUM(GREATEST(COALESCE(watch_seconds, 0), 0)) FILTER (WHERE created_at >= $2 AND created_at < $3), 0),
				COALESCE(SUM(GREATEST(COALESCE(watch_seconds, 0), 0)) FILTER (WHERE created_at >= $1 AND created_at < $2), 0)
			FROM public.video_access_logs
			WHERE created_at >= $1 AND created_at < $3
		`, yesterdayStart, todayStart, todayEnd).Scan(
			&data.ActiveToday, &data.ActiveYesterday, &data.TodaySeconds, &data.YesterdaySeconds,
		); err != nil {
			return err
		}
		offset := (query.Page - 1) * query.PageSize
		rows, err := tx.QueryContext(ctx, `
			SELECT profiles.id::text, profiles.username,
				COALESCE(profiles.display_name, ''), COALESCE(profiles.avatar_url, ''),
				profiles.vip_level, profiles.is_admin, profiles.created_at,
				COALESCE(stats.today_seconds, 0), COALESCE(stats.yesterday_seconds, 0),
				COALESCE(stats.video_count, 0), stats.recent_at
			FROM public.user_profiles AS profiles
			LEFT JOIN (
				SELECT user_id,
					SUM(GREATEST(COALESCE(watch_seconds, 0), 0)) FILTER (WHERE created_at >= $3 AND created_at < $4) AS today_seconds,
					SUM(GREATEST(COALESCE(watch_seconds, 0), 0)) FILTER (WHERE created_at >= $2 AND created_at < $3) AS yesterday_seconds,
					COUNT(DISTINCT video_id) AS video_count,
					MAX(created_at) AS recent_at
				FROM public.video_access_logs
				WHERE created_at >= $2 AND created_at < $4
				GROUP BY user_id
			) AS stats ON stats.user_id = profiles.id
			WHERE $1 = ''
				OR profiles.username ILIKE $5 ESCAPE '\'
				OR COALESCE(profiles.display_name, '') ILIKE $5 ESCAPE '\'
				OR profiles.id::text ILIKE $5 ESCAPE '\'
			ORDER BY `+studyOrder(query)+`
			LIMIT $6 OFFSET $7
		`, query.Keyword, yesterdayStart, todayStart, todayEnd, like, query.PageSize, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		data.Rows = []platform.AdminStudyRow{}
		for rows.Next() {
			var row platform.AdminStudyRow
			var vip sql.NullInt64
			var admin sql.NullBool
			var created, recent sql.NullTime
			if err := rows.Scan(
				&row.ID, &row.Username, &row.DisplayName, &row.AvatarURL,
				&vip, &admin, &created, &row.Today, &row.Yesterday, &row.Videos, &recent,
			); err != nil {
				return err
			}
			if vip.Valid {
				row.VIPLevel = vip.Int64
			}
			row.IsAdmin = admin.Valid && admin.Bool
			if created.Valid {
				value := created.Time
				row.CreatedAt = &value
			}
			if recent.Valid {
				value := recent.Time
				row.Recent = &value
			}
			data.Rows = append(data.Rows, row)
		}
		return rows.Err()
	})
	return data, mapDB(err)
}

func studyOrder(query platform.AdminStudyQuery) string {
	direction := "ASC"
	if query.Desc {
		direction = "DESC"
	}
	switch query.Sort {
	case "today":
		return "COALESCE(stats.today_seconds, 0) " + direction + ", profiles.id ASC"
	case "yesterday":
		return "COALESCE(stats.yesterday_seconds, 0) " + direction + ", profiles.id ASC"
	case "total":
		return "(COALESCE(stats.today_seconds, 0) + COALESCE(stats.yesterday_seconds, 0)) " + direction + ", profiles.id ASC"
	case "videos":
		return "COALESCE(stats.video_count, 0) " + direction + ", profiles.id ASC"
	default:
		return "profiles.created_at " + direction + " NULLS LAST, profiles.id ASC"
	}
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
