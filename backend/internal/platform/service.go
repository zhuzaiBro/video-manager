package platform

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/zood/video-manager/internal/platform/content"
	"github.com/zood/video-manager/internal/support"
)

const (
	jobPageSize             = 24
	monthlyFreeUnlockLimit  = 3
	membershipPlanPrice     = 129
	chatRateLimit           = 5
	publicJobColumns        = "id,title,company_name,company_logo,company_website,location,office_mode_name,work_type_name,min_salary,max_salary,salary_currency,pay_period,source_created_at,last_synced_at,description,requirements,benefits,extra_content,tags,status,has_contact"
	publicJobListColumns    = "id,title,company_name,company_logo,location,office_mode_name,work_type_name,min_salary,max_salary,salary_currency,pay_period,source_created_at,last_synced_at,description,tags"
	jobContactColumnsSelect = "id,title,company_name,email,phone,wechat,telegram"
)

type Service struct {
	support        *support.Service
	source         Source
	storage        ObjectStorage
	dataSource     string
	privileged     Privileged
	telegram       TelegramSender
	webhookSecret  string
	setupSecret    string
	durationLookup func(context.Context, string) int
	catalog        *content.Catalog
}

func NewService(source Source, storage ObjectStorage, dataSource string) *Service {
	if dataSource == "" {
		dataSource = "supabase"
	}
	return &Service{source: source, storage: storage, dataSource: dataSource}
}

func (s *Service) DataSource() string { return s.dataSource }

func (s *Service) SetPrivileged(p Privileged)          { s.privileged = p }
func (s *Service) SetSupport(support *support.Service) { s.support = support }

func (s *Service) SetTelegram(sender TelegramSender) { s.telegram = sender }
func (s *Service) SetTelegramSecrets(webhook, setup string) {
	s.webhookSecret = webhook
	s.setupSecret = setup
}
func (s *Service) SetDurationLookup(lookup func(context.Context, string) int) {
	s.durationLookup = lookup
}
func (s *Service) SetCatalog(catalog content.Catalog) { s.catalog = &catalog }

func (s *Service) requireUser(caller Caller) error {
	if !caller.SignedIn() || !IsUUID(caller.UserID) {
		return unauthorized("未登录")
	}
	return nil
}

func (s *Service) requireAdmin(ctx context.Context, caller Caller) error {
	if err := s.requireUser(caller); err != nil {
		return err
	}
	result, err := s.source.Select(ctx, caller, Query{
		Table:   "user_profiles",
		Select:  "id,is_admin",
		Filters: []Filter{{Column: "id", Op: "eq", Value: caller.UserID}},
		Limit:   1,
	})
	if err != nil {
		return err
	}
	if len(result.Rows) == 0 || !truthy(result.Rows[0]["is_admin"]) {
		return forbidden("无权访问")
	}
	return nil
}

func (s *Service) one(ctx context.Context, caller Caller, query Query) (map[string]any, error) {
	result, err := s.source.Select(ctx, caller, query)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}
	return result.Rows[0], nil
}

func (s *Service) many(ctx context.Context, caller Caller, query Query) (Result, error) {
	return s.source.Select(ctx, caller, query)
}

func (s *Service) ListPosts(ctx context.Context, caller Caller, limit int) (Result, error) {
	if s.catalog != nil {
		rows, count, err := s.catalog.ListPosts(ctx, caller.UserID, clampLimit(limit, 10, 50))
		if err != nil {
			return Result{}, err
		}
		return Result{Rows: rows, Count: &count}, nil
	}
	return s.many(ctx, caller, Query{
		Table:  "posts",
		Select: "id,title,slug,excerpt,banner,content,published,is_public,created_at,updated_at,author_id,author:user_profiles!fk_user_profiles_posts(username,display_name,avatar_url)",
		Filters: []Filter{
			{Column: "published", Op: "eq", Value: "true"},
			{Column: "is_public", Op: "eq", Value: "true"},
		},
		Orders: []Order{{Column: "created_at", Desc: true}},
		Limit:  clampLimit(limit, 10, 50),
		Count:  true,
	})
}

func (s *Service) GetPost(ctx context.Context, caller Caller, slug string) (map[string]any, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" || len(slug) > 200 {
		return nil, invalid("文章标识不合法")
	}
	if s.catalog != nil {
		column := "slug"
		if IsUUID(slug) {
			column = "id"
		} else if !safeScalar(slug) {
			return nil, invalid("文章标识不合法")
		}
		row, err := s.catalog.FindPublishedPost(ctx, caller.UserID, column, slug)
		if err != nil {
			return nil, err
		}
		if row == nil && IsUUID(slug) {
			row, err = s.catalog.FindPublishedPost(ctx, caller.UserID, "slug", slug)
		}
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, notFound("文章不存在")
		}
		return row, nil
	}
	filters := []Filter{
		{Column: "published", Op: "eq", Value: "true"},
		{Column: "is_public", Op: "eq", Value: "true"},
	}
	if IsUUID(slug) {
		filters = append(filters, Filter{Op: "or_ilike", Columns: []string{"slug"}, Value: slug})
		// UUID lookup is exact, not ilike. Handled below.
		filters = filters[:2]
		filters = append(filters, Filter{Column: "id", Op: "eq", Value: slug})
	} else if safeScalar(slug) {
		filters = append(filters, Filter{Column: "slug", Op: "eq", Value: slug})
	} else {
		return nil, invalid("文章标识不合法")
	}
	row, err := s.one(ctx, caller, Query{
		Table:   "posts",
		Select:  "id,title,slug,excerpt,banner,content,published,is_public,created_at,updated_at,author_id,author:user_profiles!fk_user_profiles_posts(username,display_name,avatar_url,bio)",
		Filters: filters,
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if row == nil && IsUUID(slug) {
		row, err = s.one(ctx, caller, Query{
			Table:  "posts",
			Select: "id,title,slug,excerpt,banner,content,published,is_public,created_at,updated_at,author_id,author:user_profiles!fk_user_profiles_posts(username,display_name,avatar_url,bio)",
			Filters: []Filter{
				{Column: "published", Op: "eq", Value: "true"},
				{Column: "is_public", Op: "eq", Value: "true"},
				{Column: "slug", Op: "eq", Value: slug},
			},
			Limit: 1,
		})
	}
	if row == nil && err == nil {
		return nil, notFound("文章不存在")
	}
	return row, err
}

func (s *Service) ListCategories(ctx context.Context, caller Caller) (Result, error) {
	if s.catalog != nil {
		rows, err := s.catalog.ListCategories(ctx, caller.UserID)
		if err != nil {
			return Result{}, err
		}
		count := len(rows)
		return Result{Rows: rows, Count: &count}, nil
	}
	return s.many(ctx, caller, Query{
		Table:  "post_cates",
		Select: "id,name,slug,description,created_at,post_cate_relations(post_id)",
		Orders: []Order{{Column: "name"}},
		Limit:  200,
	})
}

func (s *Service) GetCategory(ctx context.Context, caller Caller, slug string) (map[string]any, error) {
	if !safeFilterValue(slug) {
		return nil, invalid("分类标识不合法")
	}
	if s.catalog != nil {
		row, err := s.catalog.GetCategory(ctx, caller.UserID, slug)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, notFound("分类不存在")
		}
		return row, nil
	}
	category, err := s.one(ctx, caller, Query{
		Table:   "post_cates",
		Select:  "id,name,slug,description",
		Filters: []Filter{{Column: "slug", Op: "eq", Value: slug}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if category == nil {
		return nil, notFound("分类不存在")
	}
	relations, err := s.many(ctx, caller, Query{
		Table:   "post_cate_relations",
		Select:  "post_id,posts(id,title,slug,excerpt,banner,published,is_public,created_at,author:user_profiles!fk_user_profiles_posts(username,display_name,avatar_url))",
		Filters: []Filter{{Column: "cate_id", Op: "eq", Value: stringField(category, "id")}},
		Limit:   100,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"category": category, "relations": relations.Rows}, nil
}

func (s *Service) PostCategories(ctx context.Context, caller Caller, postID string) (Result, error) {
	if !IsUUID(postID) {
		return Result{}, invalid("文章标识不合法")
	}
	return s.many(ctx, caller, Query{
		Table:   "post_cate_relations",
		Select:  "post_id,cate_id,post_cates(id,name,slug)",
		Filters: []Filter{{Column: "post_id", Op: "eq", Value: postID}},
		Limit:   50,
	})
}

func (s *Service) ListComments(ctx context.Context, caller Caller, postID string) (Result, error) {
	if !IsUUID(postID) {
		return Result{}, invalid("文章标识不合法")
	}
	return s.many(ctx, caller, Query{
		Table:   "post_comments",
		Select:  "id,content,created_at,updated_at,is_deleted,parent_id,post_id,user_id,user_profiles!fk_post_comments_user(username,display_name,avatar_url)",
		Filters: []Filter{{Column: "post_id", Op: "eq", Value: postID}, {Column: "is_deleted", Op: "eq", Value: "false"}},
		Orders:  []Order{{Column: "created_at"}},
		Limit:   200,
	})
}

func (s *Service) CreateComment(ctx context.Context, caller Caller, postID, content, parentID string) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	if !IsUUID(postID) {
		return nil, invalid("文章标识不合法")
	}
	content = strings.TrimSpace(content)
	if content == "" || len(content) > 4000 {
		return nil, invalid("评论内容不合法")
	}
	payload := map[string]any{
		"post_id":   postID,
		"user_id":   caller.UserID,
		"content":   content,
		"parent_id": nil,
	}
	if parentID != "" {
		if !IsUUID(parentID) {
			return nil, invalid("回复标识不合法")
		}
		payload["parent_id"] = parentID
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table:  "post_comments",
		Select: "id,content,created_at,parent_id,post_id,user_id",
	}, payload)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权发表评论")
	}
	return result.Rows[0], nil
}

func (s *Service) CreatePost(ctx context.Context, caller Caller, input map[string]any) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	title := stringField(input, "title")
	slug := stringField(input, "slug")
	content := stringField(input, "content")
	if title == "" || slug == "" || content == "" || !safeScalar(slug) {
		return nil, invalid("文章标题、标识和正文不能为空")
	}
	payload := map[string]any{
		"title":     title,
		"slug":      slug,
		"content":   content,
		"excerpt":   nullable(stringField(input, "excerpt")),
		"banner":    nullable(stringField(input, "banner")),
		"author_id": caller.UserID,
		"published": boolField(input, "published"),
		"is_public": boolField(input, "is_public"),
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table:  "posts",
		Select: "id,title,slug,excerpt,banner,published,is_public,author_id,created_at",
	}, payload)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权发布文章")
	}
	return result.Rows[0], nil
}

func (s *Service) GetProfile(ctx context.Context, caller Caller) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	row, err := s.one(ctx, caller, Query{
		Table:   "user_profiles",
		Select:  "id,username,display_name,avatar_url,bio,is_admin,vip_level,vip_expires_at,created_at,updated_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: caller.UserID}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, notFound("个人资料不存在")
	}
	return row, nil
}

func (s *Service) SaveProfile(ctx context.Context, caller Caller, input map[string]any, create bool) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	payload := map[string]any{
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	for _, key := range []string{"username", "display_name", "avatar_url", "bio"} {
		if _, ok := input[key]; ok {
			payload[key] = nullable(stringField(input, key))
		}
	}
	if create {
		username := stringField(input, "username")
		if username == "" || len(username) > 80 {
			return nil, invalid("用户名不合法")
		}
		payload["id"] = caller.UserID
		payload["username"] = username
		result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
			Table:      "user_profiles",
			Select:     "id,username,display_name,avatar_url,bio,is_admin,vip_level,vip_expires_at,created_at,updated_at",
			OnConflict: "id",
		}, payload)
		if err != nil {
			return nil, err
		}
		if len(result.Rows) == 0 {
			return s.GetProfile(ctx, caller)
		}
		return result.Rows[0], nil
	}
	delete(payload, "id")
	result, err := s.source.Write(ctx, caller, http.MethodPatch, Query{
		Table:   "user_profiles",
		Select:  "id,username,display_name,avatar_url,bio,is_admin,vip_level,vip_expires_at,created_at,updated_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: caller.UserID}},
	}, payload)
	if err != nil {
		return nil, err
	}
	return result.Rows[0], nil
}

func (s *Service) ListJobs(ctx context.Context, caller Caller, keyword string, remoteOnly bool, page int) (Result, error) {
	if page < 1 {
		page = 1
	}
	if page > 400 {
		page = 400
	}
	if s.catalog != nil {
		rows, count, err := s.catalog.ListJobs(ctx, caller.UserID, keyword, remoteOnly, page, jobPageSize)
		if err != nil {
			return Result{}, err
		}
		return Result{Rows: rows, Count: &count}, nil
	}
	// Public job policy is USING (true). status defaults to 0 and is not a visibility flag.
	filters := []Filter{}
	if strings.TrimSpace(keyword) != "" {
		filters = append(filters, Filter{Op: "or_ilike", Columns: []string{"title", "company_name", "description"}, Value: keyword})
	}
	if remoteOnly {
		filters = append(filters, Filter{Column: "is_remote", Op: "eq", Value: "true"})
	}
	result, err := s.many(ctx, caller, Query{
		Table:   "job_listings",
		Select:  publicJobListColumns,
		Filters: filters,
		Orders:  []Order{{Column: "source_created_at", Desc: true}, {Column: "last_synced_at", Desc: true}, {Column: "id"}},
		Limit:   jobPageSize,
		Offset:  (page - 1) * jobPageSize,
		Count:   true,
	})
	if err != nil {
		return Result{}, err
	}
	for _, row := range result.Rows {
		for column := range jobContactColumns {
			delete(row, column)
		}
	}
	return result, nil
}

func (s *Service) GetJob(ctx context.Context, caller Caller, id string) (map[string]any, error) {
	if !IsUUID(id) {
		return nil, invalid("岗位标识不合法")
	}
	row, err := s.one(ctx, caller, Query{
		Table:   "job_listings",
		Select:  publicJobColumns,
		Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, notFound("岗位不存在")
	}
	for column := range jobContactColumns {
		delete(row, column)
	}
	return row, nil
}

func (s *Service) JobContact(ctx context.Context, caller Caller, id string, statusOnly bool) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	if !IsUUID(id) {
		return nil, invalid("岗位标识不合法")
	}
	profile, err := s.one(ctx, caller, Query{
		Table:   "user_profiles",
		Select:  "id,vip_level,vip_expires_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: caller.UserID}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	member := activeMember(profile)
	job, err := s.one(ctx, caller, Query{
		Table:   "job_listings",
		Select:  jobContactColumnsSelect,
		Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, notFound("岗位不存在")
	}
	for _, column := range []string{"email", "phone", "wechat", "telegram"} {
		if strings.TrimSpace(stringField(job, column)) != "" {
			return s.unlockJobContact(ctx, caller, id, job, member, statusOnly)
		}
	}
	return map[string]any{"unlocked": false, "available": false, "message": "招聘方暂未公开联系方式"}, nil
}

func (s *Service) unlockJobContact(ctx context.Context, caller Caller, id string, job map[string]any, member, statusOnly bool) (map[string]any, error) {
	monthStart := shanghaiMonthStart(time.Now())
	existing, err := s.one(ctx, caller, Query{
		Table:  "job_contact_unlocks",
		Select: "id,access_type,month_start",
		Filters: []Filter{
			{Column: "user_id", Op: "eq", Value: caller.UserID},
			{Column: "job_id", Op: "eq", Value: id},
		},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	usedResult, err := s.many(ctx, caller, Query{
		Table:  "job_contact_unlocks",
		Select: "id",
		Filters: []Filter{
			{Column: "user_id", Op: "eq", Value: caller.UserID},
			{Column: "month_start", Op: "eq", Value: monthStart},
			{Column: "access_type", Op: "eq", Value: "quota"},
		},
		Limit: 100,
		Count: true,
	})
	if err != nil {
		return nil, err
	}
	used := 0
	if usedResult.Count != nil {
		used = *usedResult.Count
	} else {
		used = len(usedResult.Rows)
	}
	invalidMember := existing != nil && existing["access_type"] == "member" && !member
	unlocked := existing != nil && !invalidMember
	remaining := monthlyFreeUnlockLimit - used
	if remaining < 0 {
		remaining = 0
	}
	reveal := member || unlocked
	response := map[string]any{
		"job": map[string]any{
			"id":          job["id"],
			"title":       job["title"],
			"companyName": job["company_name"],
		},
		"isMember":  member,
		"unlocked":  reveal,
		"limit":     monthlyFreeUnlockLimit,
		"used":      used,
		"remaining": remaining,
	}
	if member {
		response["remaining"] = nil
	}
	if statusOnly {
		if reveal {
			response["contacts"] = contactsOf(job)
		}
		return response, nil
	}
	if !member && !unlocked && remaining <= 0 {
		return nil, NewError(http.StatusForbidden, "MONTHLY_LIMIT_REACHED", "本月免费查看次数已用完，开通会员后可无限查看岗位联系方式")
	}
	if !unlocked {
		accessType := "quota"
		if member {
			accessType = "member"
		}
		payload := map[string]any{
			"user_id":     caller.UserID,
			"job_id":      id,
			"month_start": monthStart,
			"access_type": accessType,
		}
		if invalidMember {
			_, err = s.source.Write(ctx, caller, http.MethodPatch, Query{
				Table:   "job_contact_unlocks",
				Select:  "id",
				Filters: []Filter{{Column: "id", Op: "eq", Value: stringField(existing, "id")}, {Column: "user_id", Op: "eq", Value: caller.UserID}},
			}, map[string]any{"month_start": monthStart, "access_type": accessType})
		} else {
			_, err = s.source.Write(ctx, caller, http.MethodPost, Query{Table: "job_contact_unlocks", Select: "id"}, payload)
		}
		if err != nil {
			if apiErr, ok := err.(*Error); ok && apiErr.Code == "23505" {
				err = nil
			} else {
				return nil, err
			}
		}
		if !member {
			remaining--
			if remaining < 0 {
				remaining = 0
			}
			used++
		}
	}
	response["contacts"] = contactsOf(job)
	response["unlocked"] = true
	response["used"] = used
	response["remaining"] = remaining
	if member {
		response["remaining"] = nil
	}
	return response, nil
}

func (s *Service) Favorite(ctx context.Context, caller Caller, questionID string, enabled bool) error {
	if err := s.requireUser(caller); err != nil {
		return err
	}
	if !IsUUID(questionID) {
		return invalid("题目标识不合法")
	}
	if !enabled {
		_, err := s.source.Write(ctx, caller, http.MethodDelete, Query{
			Table:  "interview_question_favorites",
			Select: "id",
			Filters: []Filter{
				{Column: "user_id", Op: "eq", Value: caller.UserID},
				{Column: "question_id", Op: "eq", Value: questionID},
			},
		}, map[string]any{})
		if apiErr, ok := err.(*Error); ok && apiErr.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	_, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table:      "interview_question_favorites",
		Select:     "id,user_id,question_id",
		OnConflict: "user_id,question_id",
	}, map[string]any{"user_id": caller.UserID, "question_id": questionID})
	return err
}

func (s *Service) HasFavorite(ctx context.Context, caller Caller, questionID string) (bool, error) {
	if err := s.requireUser(caller); err != nil {
		return false, err
	}
	if !IsUUID(questionID) {
		return false, invalid("题目标识不合法")
	}
	row, err := s.one(ctx, caller, Query{
		Table:  "interview_question_favorites",
		Select: "id",
		Filters: []Filter{
			{Column: "user_id", Op: "eq", Value: caller.UserID},
			{Column: "question_id", Op: "eq", Value: questionID},
		},
		Limit: 1,
	})
	return row != nil, err
}

func (s *Service) SearchQuestions(ctx context.Context, caller Caller, keyword string) (Result, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return Result{Rows: []map[string]any{}}, nil
	}
	if len(keyword) > 100 {
		return Result{}, invalid("搜索关键词过长")
	}
	return s.many(ctx, caller, Query{
		Table:   "interview_question",
		Select:  "id,title,content,difficulty,is_vip,collection_id,interview_collections(title)",
		Filters: []Filter{{Op: "or_ilike", Columns: []string{"title", "content"}, Value: keyword}},
		Orders:  []Order{{Column: "sort"}},
		Limit:   50,
	})
}

func (s *Service) CreateMessage(ctx context.Context, caller Caller, sessionID, content, pageURL string) (map[string]any, error) {
	if !IsUUID(sessionID) {
		return nil, invalid("会话标识不合法")
	}
	if !caller.SignedIn() && s.support != nil {
		ok, err := s.support.GuestAllowed(ctx, sessionID)
		if err != nil {
			return nil, NewError(503, "SUPPORT_UNAVAILABLE", "客服暂时不可用")
		}
		if !ok {
			return nil, forbidden("会话身份不匹配")
		}
	}
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > 2000 {
		return nil, invalid("消息内容不合法")
	}
	if caller.SignedIn() && sessionID != caller.UserID {
		return nil, forbidden("会话身份不匹配")
	}
	since := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	recent, err := s.many(ctx, caller, Query{
		Table:  "messages",
		Select: "id",
		Filters: []Filter{
			{Column: "session_id", Op: "eq", Value: sessionID},
			{Column: "sender_type", Op: "eq", Value: "user"},
			{Column: "created_at", Op: "gte", Value: since},
		},
		Limit: 10,
		Count: true,
	})
	if err != nil {
		return nil, err
	}
	count := len(recent.Rows)
	if recent.Count != nil {
		count = *recent.Count
	}
	if count >= chatRateLimit {
		return nil, NewError(http.StatusTooManyRequests, "RATE_LIMIT", "发送得有点快，请稍后再试")
	}
	var userID any
	if caller.SignedIn() {
		userID = caller.UserID
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table:  "messages",
		Select: "id,session_id,user_id,sender_type,content,created_at",
	}, map[string]any{
		"session_id":  sessionID,
		"user_id":     userID,
		"sender_type": "user",
		"content":     content,
	})
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权发送消息")
	}
	result.Rows[0]["pageUrl"] = pageURL
	if s.support != nil {
		s.support.Notify(sessionID)
		if s.telegram == nil || s.support.HasBot(ctx) {
			return result.Rows[0], nil
		}
	}
	forwarded := s.forwardUserMessage(ctx, stringField(result.Rows[0], "id"), sessionID, content, pageURL, caller)
	result.Rows[0]["forwarded"] = forwarded
	return result.Rows[0], nil
}

func (s *Service) ListMessages(ctx context.Context, caller Caller, sessionID string) (Result, error) {
	if !IsUUID(sessionID) {
		return Result{}, invalid("会话标识不合法")
	}
	if !caller.SignedIn() && s.support != nil {
		ok, err := s.support.GuestAllowed(ctx, sessionID)
		if err != nil {
			return Result{}, NewError(503, "SUPPORT_UNAVAILABLE", "客服暂时不可用")
		}
		if !ok {
			return Result{}, forbidden("会话身份不匹配")
		}
	}
	if caller.SignedIn() && sessionID != caller.UserID {
		if err := s.requireAdmin(ctx, caller); err != nil {
			return Result{}, err
		}
	}
	result, err := s.many(ctx, caller, Query{
		Table:   "messages",
		Select:  "id,session_id,user_id,sender_type,content,created_at",
		Filters: []Filter{{Column: "session_id", Op: "eq", Value: sessionID}},
		Orders:  []Order{{Column: "created_at", Desc: true}, {Column: "id", Desc: true}},
		Limit:   200,
	})
	for i, j := 0, len(result.Rows)-1; i < j; i, j = i+1, j-1 {
		result.Rows[i], result.Rows[j] = result.Rows[j], result.Rows[i]
	}
	return result, err
}

func contactsOf(job map[string]any) map[string]any {
	return map[string]any{
		"email":    job["email"],
		"phone":    job["phone"],
		"wechat":   job["wechat"],
		"telegram": job["telegram"],
	}
}

func activeMember(profile map[string]any) bool {
	if profile == nil {
		return false
	}
	level, _ := numberValue(profile["vip_level"])
	if level <= 0 {
		return false
	}
	expires, _ := profile["vip_expires_at"].(string)
	if expires == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		return false
	}
	return parsed.After(time.Now())
}

func shanghaiMonthStart(now time.Time) string {
	shanghai := now.UTC().Add(8 * time.Hour)
	return shanghai.Format("2006-01") + "-01"
}

func stringField(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	value, _ := row[key].(string)
	return strings.TrimSpace(value)
}

func boolField(row map[string]any, key string) bool {
	value, _ := row[key].(bool)
	return value
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func truthy(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}
