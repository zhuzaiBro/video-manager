package platform

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	adminStudyDefaultPage = 1
	adminStudyDefaultSize = 12
	adminStudyMaxSize     = 50
	adminStudyMaxPage     = 10000
	adminStudyMaxKeyword  = 80
)

// AdminStudyQuery is the allowlisted page request for the admin user list.
// Sort and page size are not free-form SQL.
type AdminStudyQuery struct {
	Page     int
	PageSize int
	Keyword  string
	Sort     string
	Desc     bool
}

// AdminStudyRow is one profile plus study totals. It never carries auth
// secrets, phone numbers, or log IP addresses.
type AdminStudyRow struct {
	ID          string
	Username    string
	DisplayName string
	AvatarURL   string
	VIPLevel    any
	IsAdmin     bool
	CreatedAt   *time.Time
	Today       int64
	Yesterday   int64
	Videos      int64
	Recent      *time.Time
}

// AdminStudyData is one stable page plus totals for the whole list.
type AdminStudyData struct {
	Rows             []AdminStudyRow
	FilteredTotal    int64
	UserCount        int64
	ActiveToday      int64
	ActiveYesterday  int64
	TodaySeconds     int64
	YesterdaySeconds int64
}

func ParseAdminStudyQuery(values url.Values) (AdminStudyQuery, error) {
	page, err := optionalPositive(values.Get("page"), adminStudyDefaultPage, adminStudyMaxPage)
	if err != nil {
		return AdminStudyQuery{}, invalid("页码不合法")
	}
	pageSize, err := optionalPositive(values.Get("pageSize"), adminStudyDefaultSize, adminStudyMaxSize)
	if err != nil {
		return AdminStudyQuery{}, invalid("分页大小不合法")
	}
	keyword, err := cleanStudyKeyword(values.Get("q"))
	if err != nil {
		return AdminStudyQuery{}, err
	}
	sort := values.Get("sort")
	if sort == "" {
		sort = "created_at"
	}
	switch sort {
	case "created_at", "today", "yesterday", "total", "videos":
	default:
		return AdminStudyQuery{}, invalid("排序字段不合法")
	}
	order := values.Get("order")
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return AdminStudyQuery{}, invalid("排序方向不合法")
	}
	return AdminStudyQuery{
		Page: page, PageSize: pageSize, Keyword: keyword, Sort: sort, Desc: order == "desc",
	}, nil
}

func optionalPositive(value string, fallback, max int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > max {
		return 0, invalid("分页参数不合法")
	}
	return parsed, nil
}

func cleanStudyKeyword(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if utf8.RuneCountInString(value) > adminStudyMaxKeyword {
		return "", invalid("搜索词过长")
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return "", invalid("搜索词不合法")
		}
	}
	return value, nil
}

func (s *Service) AdminStudy(ctx context.Context, caller Caller, raw url.Values) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	query, err := ParseAdminStudyQuery(raw)
	if err != nil {
		return nil, err
	}
	reader, ok := s.privileged.(adminStudyReader)
	if !ok {
		return nil, NewError(http.StatusServiceUnavailable, "STUDY_UNAVAILABLE", "管理员用户列表需要本地数据库")
	}
	ctx = WithCaller(ctx, caller)
	now := time.Now()
	today := shanghaiDay(now, 0)
	yesterday := shanghaiDay(now, -1)
	data, err := reader.ReadAdminStudy(ctx, query, yesterday.start, today.start, today.end)
	if err != nil {
		return nil, err
	}
	if data.Rows == nil {
		data.Rows = []AdminStudyRow{}
	}
	if len(data.Rows) > query.PageSize {
		return nil, NewError(http.StatusInternalServerError, "RESULT_TRUNCATED", "用户分页结果超过页大小")
	}
	users := make([]map[string]any, 0, len(data.Rows))
	for _, row := range data.Rows {
		users = append(users, map[string]any{
			"id": row.ID, "email": nil, "phone": nil,
			"username": row.Username, "displayName": nullable(row.DisplayName), "avatarUrl": nullable(row.AvatarURL),
			"provider": nil, "providers": []any{}, "role": nil,
			"emailConfirmedAt": nil, "phoneConfirmedAt": nil, "lastSignInAt": nil,
			"isSsoUser": false, "isAnonymous": false, "hasProfile": true,
			"vipLevel": row.VIPLevel, "isAdmin": row.IsAdmin, "createdAt": formatStudyTime(row.CreatedAt),
			"todayStudySeconds": row.Today, "yesterdayStudySeconds": row.Yesterday,
			"totalStudySeconds": row.Today + row.Yesterday, "uniqueVideoCount": row.Videos,
			"recentStudyAt": formatStudyTime(row.Recent),
		})
	}
	return map[string]any{
		"users": users,
		"summary": map[string]any{
			"userCount": data.UserCount, "missingProfileCount": 0,
			"activeTodayCount": data.ActiveToday, "activeYesterdayCount": data.ActiveYesterday,
			"todayStudySeconds": data.TodaySeconds, "yesterdayStudySeconds": data.YesterdaySeconds,
		},
		"page": map[string]any{
			"page": query.Page, "pageSize": query.PageSize, "total": data.FilteredTotal,
		},
		"source": "platform-admin",
		"ranges": map[string]any{
			"today":     map[string]any{"label": today.label, "start": today.start.Format(time.RFC3339), "end": today.end.Format(time.RFC3339)},
			"yesterday": map[string]any{"label": yesterday.label, "start": yesterday.start.Format(time.RFC3339), "end": yesterday.end.Format(time.RFC3339)},
		},
	}, nil
}

type adminStudyReader interface {
	ReadAdminStudy(ctx context.Context, query AdminStudyQuery, yesterdayStart, todayStart, todayEnd time.Time) (AdminStudyData, error)
}

func formatStudyTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}
