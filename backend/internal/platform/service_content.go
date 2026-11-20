package platform

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (s *Service) ListCourses(ctx context.Context, caller Caller, status string, admin bool) (Result, error) {
	filters := []Filter{}
	if admin {
		if err := s.requireAdmin(ctx, caller); err != nil {
			return Result{}, err
		}
	} else {
		status = "published"
	}
	if status != "" && status != "all" {
		if status != "published" && status != "draft" && status != "archived" {
			return Result{}, invalid("无效的课程状态")
		}
		filters = append(filters, Filter{Column: "status", Op: "eq", Value: status})
	}
	result, err := s.many(ctx, caller, Query{
		Table:   "courses",
		Select:  "id,title,description,cover_image_url,price,is_free,status,sort_order,created_by,created_at,updated_at",
		Filters: filters,
		Orders:  []Order{{Column: "sort_order"}, {Column: "created_at"}},
		Limit:   200,
	})
	if err != nil || admin || len(result.Rows) == 0 {
		return result, err
	}
	if err := s.attachPublishedStats(ctx, caller, result.Rows); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) CourseDetail(ctx context.Context, caller Caller, courseID string) (map[string]any, error) {
	if !IsUUID(courseID) {
		return nil, invalid("课程标识不合法")
	}
	course, err := s.one(ctx, caller, Query{
		Table:   "courses",
		Select:  "id,title,description,cover_image_url,price,is_free,status,created_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: courseID}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if course == nil {
		return nil, notFound("课程不存在")
	}
	hasAccess := truthy(course["is_free"])
	accessSource := "none"
	if hasAccess {
		accessSource = "free"
	}
	loggedIn := caller.SignedIn()
	if loggedIn {
		profile, err := s.one(ctx, caller, Query{
			Table:   "user_profiles",
			Select:  "is_admin",
			Filters: []Filter{{Column: "id", Op: "eq", Value: caller.UserID}},
			Limit:   1,
		})
		if err != nil {
			return nil, err
		}
		if truthy(profile["is_admin"]) {
			hasAccess = true
			accessSource = "admin"
		} else if !hasAccess {
			enrollment, err := s.one(ctx, caller, Query{
				Table:  "course_enrollments",
				Select: "id",
				Filters: []Filter{
					{Column: "course_id", Op: "eq", Value: courseID},
					{Column: "user_id", Op: "eq", Value: caller.UserID},
					{Column: "status", Op: "eq", Value: "active"},
				},
				Limit: 1,
			})
			if err != nil {
				return nil, err
			}
			if enrollment != nil {
				hasAccess = true
				accessSource = "enrollment"
			}
		}
	}
	chapters, err := s.many(ctx, caller, Query{
		Table:   "chapters",
		Select:  "id,course_id,title,description,sort_order",
		Filters: []Filter{{Column: "course_id", Op: "eq", Value: courseID}},
		Orders:  []Order{{Column: "sort_order"}},
		Limit:   200,
	})
	if err != nil {
		return nil, err
	}
	chapterIDs := make([]string, 0, len(chapters.Rows))
	for _, chapter := range chapters.Rows {
		if id := stringField(chapter, "id"); IsUUID(id) {
			chapterIDs = append(chapterIDs, id)
		}
	}
	lessons := Result{}
	if len(chapterIDs) > 0 {
		lessons, err = s.many(ctx, caller, Query{
			Table:   "lessons",
			Select:  "id,chapter_id,title,description,courseware_name,courseware_url,content_html,content_markdown,video_id,video_url,duration,is_free,sort_order",
			Filters: []Filter{{Column: "chapter_id", Op: "in", Values: chapterIDs}},
			Orders:  []Order{{Column: "sort_order"}},
			Limit:   200,
		})
		if err != nil {
			return nil, err
		}
	}
	grouped := make([]map[string]any, 0, len(chapters.Rows))
	for _, chapter := range chapters.Rows {
		items := []map[string]any{}
		for _, lesson := range lessons.Rows {
			if lesson["chapter_id"] != chapter["id"] {
				continue
			}
			free := truthy(lesson["is_free"])
			videoID := strings.TrimSpace(stringField(lesson, "video_id"))
			hasVideo := videoID != ""
			canWatch := hasVideo && loggedIn && (hasAccess || free)
			var reason any
			if hasVideo && !loggedIn {
				reason = "login"
			} else if hasVideo && !canWatch {
				reason = "purchase"
			}
			item := map[string]any{
				"id":              lesson["id"],
				"title":           lesson["title"],
				"description":     lesson["description"],
				"coursewareName":  lesson["courseware_name"],
				"coursewareUrl":   lesson["courseware_url"],
				"contentHtml":     lesson["content_html"],
				"contentMarkdown": lesson["content_markdown"],
				"durationSeconds": lesson["duration"],
				"isFree":          free,
				"hasVideo":        hasVideo,
				"isLocked":        hasVideo && !canWatch,
				"accessReason":    reason,
				"videoUrl":        nil,
				"videoId":         nil,
				"sortOrder":       lesson["sort_order"],
			}
			if canWatch {
				item["videoUrl"] = lesson["video_url"]
				item["videoId"] = lesson["video_id"]
			}
			items = append(items, item)
		}
		grouped = append(grouped, map[string]any{
			"id":          chapter["id"],
			"title":       chapter["title"],
			"description": chapter["description"],
			"sortOrder":   chapter["sort_order"],
			"lessons":     items,
		})
	}
	return map[string]any{
		"course": map[string]any{
			"id":            course["id"],
			"title":         course["title"],
			"description":   course["description"],
			"coverImageUrl": course["cover_image_url"],
			"price":         course["price"],
			"isFree":        course["is_free"],
			"hasAccess":     hasAccess,
			"accessSource":  accessSource,
			"status":        course["status"],
			"createdAt":     course["created_at"],
		},
		"chapters": grouped,
	}, nil
}

func (s *Service) UpdateCourse(ctx context.Context, caller Caller, courseID string, input map[string]any) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	if !IsUUID(courseID) {
		return nil, invalid("课程标识不合法")
	}
	updates := map[string]any{}
	if _, ok := input["title"]; ok {
		title := stringField(input, "title")
		if title == "" {
			return nil, invalid("课程标题不能为空")
		}
		updates["title"] = title
	}
	if _, ok := input["description"]; ok {
		updates["description"] = nullable(stringField(input, "description"))
	}
	if _, ok := input["coverImageUrl"]; ok {
		updates["cover_image_url"] = nullable(stringField(input, "coverImageUrl"))
	}
	if _, ok := input["status"]; ok {
		status := stringField(input, "status")
		if status != "draft" && status != "published" && status != "archived" {
			return nil, invalid("无效的课程状态")
		}
		updates["status"] = status
	}
	if _, ok := input["isFree"]; ok {
		free := boolField(input, "isFree")
		updates["is_free"] = free
		if free {
			updates["price"] = 0
		}
	}
	if _, ok := input["price"]; ok && updates["is_free"] != true {
		price, ok := numberValue(input["price"])
		if !ok || price < 0 {
			return nil, invalid("价格不能为负数")
		}
		updates["price"] = price
	}
	if len(updates) == 0 {
		return nil, invalid("没有可更新的字段")
	}
	result, err := s.source.Write(ctx, caller, http.MethodPatch, Query{
		Table:   "courses",
		Select:  "id,title,description,cover_image_url,price,is_free,status,sort_order,created_at,updated_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: courseID}},
	}, updates)
	if err != nil {
		return nil, err
	}
	return result.Rows[0], nil
}

func (s *Service) SaveProgress(ctx context.Context, caller Caller, input map[string]any) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	lessonID := stringField(input, "lessonId")
	if !IsUUID(lessonID) {
		return nil, invalid("缺少 lessonId")
	}
	current := nonNegative(input["currentSeconds"])
	duration := nonNegative(input["durationSeconds"])
	watch := nonNegative(input["watchSeconds"])
	percent := 0.0
	if duration > 0 {
		percent = current / duration * 100
		if percent > 100 {
			percent = 100
		}
	}
	completed := duration > 0 && percent >= 95
	var completedAt any
	if completed {
		completedAt = time.Now().UTC().Format(time.RFC3339)
	}
	payload := map[string]any{
		"user_id":          caller.UserID,
		"lesson_id":        lessonID,
		"course_id":        nullable(stringField(input, "courseId")),
		"video_id":         nullable(stringField(input, "videoId")),
		"current_seconds":  int(current),
		"duration_seconds": int(duration),
		"progress_percent": percent,
		"is_completed":     completed,
		"completed_at":     completedAt,
		"last_watched_at":  time.Now().UTC().Format(time.RFC3339),
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table:      "lesson_progress",
		Select:     "id,user_id,lesson_id,course_id,video_id,current_seconds,duration_seconds,progress_percent,is_completed,last_watched_at",
		OnConflict: "user_id,lesson_id",
	}, payload)
	if err != nil {
		return nil, err
	}
	if watch > 0 && stringField(input, "videoId") != "" {
		_, _ = s.source.Write(ctx, caller, http.MethodPost, Query{
			Table:  "video_access_logs",
			Select: "id",
		}, map[string]any{
			"user_id":       caller.UserID,
			"video_id":      stringField(input, "videoId"),
			"segment_name":  nullable(stringField(input, "segmentName")),
			"watch_seconds": int(watch),
		})
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权保存学习进度")
	}
	return result.Rows[0], nil
}

func (s *Service) ListProgress(ctx context.Context, caller Caller, courseID, lessonID string) (map[string]any, error) {
	if !caller.SignedIn() {
		return map[string]any{"progress": nil, "progresses": []any{}}, nil
	}
	filters := []Filter{{Column: "user_id", Op: "eq", Value: caller.UserID}}
	if courseID != "" {
		if !IsUUID(courseID) {
			return nil, invalid("课程标识不合法")
		}
		filters = append(filters, Filter{Column: "course_id", Op: "eq", Value: courseID})
	}
	if lessonID != "" {
		if !IsUUID(lessonID) {
			return nil, invalid("课时标识不合法")
		}
		filters = append(filters, Filter{Column: "lesson_id", Op: "eq", Value: lessonID})
	}
	result, err := s.many(ctx, caller, Query{
		Table:   "lesson_progress",
		Select:  "id,user_id,lesson_id,course_id,video_id,current_seconds,duration_seconds,progress_percent,is_completed,completed_at,last_watched_at",
		Filters: filters,
		Orders:  []Order{{Column: "last_watched_at", Desc: true}},
		Limit:   200,
	})
	if err != nil {
		return nil, err
	}
	var first any
	if len(result.Rows) > 0 {
		first = result.Rows[0]
	}
	return map[string]any{"progress": first, "progresses": result.Rows}, nil
}

func (s *Service) CreatePurchaseRequest(ctx context.Context, caller Caller, input map[string]any) (map[string]any, error) {
	courseID := stringField(input, "courseId")
	phone := stringField(input, "phone")
	wechat := stringField(input, "wechat")
	if !IsUUID(courseID) {
		return nil, invalid("缺少课程 ID")
	}
	if phone == "" {
		return nil, invalid("请填写手机号")
	}
	if wechat == "" {
		return nil, invalid("请填写微信号")
	}
	course, err := s.one(ctx, caller, Query{
		Table:   "courses",
		Select:  "id,title,price,is_free",
		Filters: []Filter{{Column: "id", Op: "eq", Value: courseID}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if course == nil {
		return nil, notFound("课程不存在")
	}
	if truthy(course["is_free"]) {
		return nil, invalid("免费课程无需购买")
	}
	var userID any
	if caller.SignedIn() {
		userID = caller.UserID
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table:  "course_purchase_requests",
		Select: "id,course_id,user_id,phone,wechat,note,status,created_at",
	}, map[string]any{
		"course_id":    courseID,
		"course_title": course["title"],
		"course_price": course["price"],
		"user_id":      userID,
		"phone":        phone,
		"wechat":       wechat,
		"note":         nullable(stringField(input, "note")),
		"status":       "pending",
	})
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权提交购买意向")
	}
	return result.Rows[0], nil
}

func (s *Service) CreateConsultation(ctx context.Context, caller Caller, input map[string]any) (map[string]any, error) {
	phone := stringField(input, "phone")
	wechat := stringField(input, "wechat")
	if phone == "" {
		return nil, invalid("请填写手机号")
	}
	if wechat == "" {
		return nil, invalid("请填写微信号")
	}
	var userID any
	if caller.SignedIn() {
		userID = caller.UserID
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table:  "membership_consultation_requests",
		Select: "id,user_id,phone,wechat,note,source,plan_price,status,created_at",
	}, map[string]any{
		"user_id":    userID,
		"phone":      phone,
		"wechat":     wechat,
		"note":       nullable(stringField(input, "note")),
		"source":     nullable(stringField(input, "source")),
		"plan_price": membershipPlanPrice,
		"status":     "pending",
	})
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权提交会员咨询")
	}
	return result.Rows[0], nil
}

func nonNegative(value any) float64 {
	number, ok := numberValue(value)
	if !ok || number < 0 {
		return 0
	}
	return float64(int(number))
}
