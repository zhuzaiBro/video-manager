package platform

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (s *Service) InterviewHome(ctx context.Context, caller Caller, tagSlug, jobSlug string) (map[string]any, error) {
	job, ok := interviewJob(jobSlug)
	if !ok {
		return nil, invalid("岗位不存在")
	}
	if job["slug"] != "programmer" {
		return map[string]any{
			"jobs":           interviewJobs,
			"job":            job,
			"tags":           []map[string]any{},
			"collections":    []map[string]any{},
			"questionCounts": map[string]int{},
			"effectiveTag":   "",
		}, nil
	}
	tags, err := s.many(ctx, caller, Query{
		Table:  "interview_tags",
		Select: "id,name,slug,sort,created_at",
		Orders: []Order{{Column: "sort"}, {Column: "created_at"}},
		Limit:  100,
	})
	if err != nil {
		return nil, err
	}
	effective := strings.TrimSpace(tagSlug)
	if effective == "" && len(tags.Rows) > 0 {
		effective = stringField(tags.Rows[0], "slug")
	}
	collections := Result{Rows: []map[string]any{}}
	if effective != "" {
		tag, err := s.one(ctx, caller, Query{
			Table:   "interview_tags",
			Select:  "id,slug",
			Filters: []Filter{{Column: "slug", Op: "eq", Value: effective}},
			Limit:   1,
		})
		if err != nil {
			return nil, err
		}
		if tag != nil {
			relations, err := s.many(ctx, caller, Query{
				Table:   "interview_collection_tags",
				Select:  "collection_id,tag_id",
				Filters: []Filter{{Column: "tag_id", Op: "eq", Value: stringField(tag, "id")}},
				Limit:   200,
			})
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(relations.Rows))
			for _, row := range relations.Rows {
				if id := stringField(row, "collection_id"); IsUUID(id) {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				collections, err = s.many(ctx, caller, Query{
					Table:   "interview_collections",
					Select:  "id,title,description,icon,sort,created_at",
					Filters: []Filter{{Column: "id", Op: "in", Values: ids}},
					Orders:  []Order{{Column: "sort"}, {Column: "created_at", Desc: true}},
					Limit:   200,
				})
				if err != nil {
					return nil, err
				}
			}
		}
	}
	counts := map[string]int{}
	if len(collections.Rows) > 0 {
		ids := make([]string, 0, len(collections.Rows))
		for _, row := range collections.Rows {
			if id := stringField(row, "id"); IsUUID(id) {
				ids = append(ids, id)
			}
		}
		questions, err := s.many(ctx, caller, Query{
			Table:   "interview_question",
			Select:  "id,collection_id",
			Filters: []Filter{{Column: "collection_id", Op: "in", Values: ids}},
			Limit:   200,
		})
		if err != nil {
			return nil, err
		}
		for _, row := range questions.Rows {
			id := stringField(row, "collection_id")
			counts[id] = counts[id] + 1
		}
	}
	return map[string]any{
		"jobs":           interviewJobs,
		"job":            job,
		"tags":           tags.Rows,
		"collections":    collections.Rows,
		"questionCounts": counts,
		"effectiveTag":   effective,
	}, nil
}

func (s *Service) GetCollection(ctx context.Context, caller Caller, id string) (map[string]any, error) {
	if !IsUUID(id) {
		return nil, invalid("题集标识不合法")
	}
	collection, err := s.one(ctx, caller, Query{
		Table:   "interview_collections",
		Select:  "id,title,description,icon,sort,created_at,updated_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if collection == nil {
		return nil, notFound("题集不存在")
	}
	questions, err := s.many(ctx, caller, Query{
		Table:   "interview_question",
		Select:  "id,title,difficulty,is_vip,vip_level_required,sort,created_at,collection_id,interview_question_tags(interview_tags(id,name))",
		Filters: []Filter{{Column: "collection_id", Op: "eq", Value: id}},
		Orders:  []Order{{Column: "sort"}, {Column: "created_at"}},
		Limit:   200,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"collection": collection, "questions": questions.Rows}, nil
}

func (s *Service) Sitemap(ctx context.Context, caller Caller) (map[string]any, error) {
	posts, err := s.many(ctx, caller, Query{
		Table: "posts", Select: "slug,updated_at",
		Filters: []Filter{{Column: "published", Op: "eq", Value: "true"}, {Column: "is_public", Op: "eq", Value: "true"}},
		Orders:  []Order{{Column: "updated_at", Desc: true}}, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	categories, err := s.many(ctx, caller, Query{Table: "post_cates", Select: "slug,created_at", Limit: 200})
	if err != nil {
		return nil, err
	}
	courses, err := s.many(ctx, caller, Query{
		Table: "courses", Select: "id,updated_at", Filters: []Filter{{Column: "status", Op: "eq", Value: "published"}}, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	collections, err := s.many(ctx, caller, Query{Table: "interview_collections", Select: "id,created_at", Limit: 200})
	if err != nil {
		return nil, err
	}
	questions, err := s.many(ctx, caller, Query{Table: "interview_question", Select: "id,updated_at", Limit: 200})
	if err != nil {
		return nil, err
	}
	tags, err := s.many(ctx, caller, Query{Table: "interview_tags", Select: "slug,created_at", Limit: 100})
	if err != nil {
		return nil, err
	}
	jobs, err := s.many(ctx, caller, Query{
		Table: "job_listings", Select: "id,updated_at,last_synced_at",
		Filters: []Filter{{Column: "status", Op: "eq", Value: "1"}},
		Orders:  []Order{{Column: "last_synced_at", Desc: true}}, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"posts": posts.Rows, "categories": categories.Rows, "courses": courses.Rows,
		"collections": collections.Rows, "questions": questions.Rows, "tags": tags.Rows, "jobs": jobs.Rows,
	}, nil
}

func (s *Service) SetPostCategories(ctx context.Context, caller Caller, postID string, categoryIDs []string) error {
	if err := s.requireUser(caller); err != nil {
		return err
	}
	if !IsUUID(postID) {
		return invalid("文章标识不合法")
	}
	post, err := s.one(ctx, caller, Query{
		Table: "posts", Select: "id,author_id",
		Filters: []Filter{{Column: "id", Op: "eq", Value: postID}, {Column: "author_id", Op: "eq", Value: caller.UserID}},
		Limit:   1,
	})
	if err != nil {
		return err
	}
	if post == nil {
		return notFound("文章不存在或无权修改")
	}
	if len(categoryIDs) > 20 {
		return invalid("分类过多")
	}
	for _, id := range categoryIDs {
		if !IsUUID(id) {
			return invalid("分类标识不合法")
		}
		if _, err := s.source.Write(ctx, caller, http.MethodPost, Query{
			Table: "post_cate_relations", Select: "post_id,cate_id", OnConflict: "post_id,cate_id",
		}, map[string]any{"post_id": postID, "cate_id": id}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetQuestion(ctx context.Context, caller Caller, id string) (map[string]any, error) {
	if !IsUUID(id) {
		return nil, invalid("题目标识不合法")
	}
	row, err := s.one(ctx, caller, Query{
		Table:   "interview_question",
		Select:  "id,title,content,difficulty,is_vip,vip_level_required,collection_id,sort,created_at,updated_at,interview_collections(id,title)",
		Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, notFound("题目不存在")
	}
	navigation, err := s.questionNavigation(ctx, caller, row)
	if err != nil {
		return nil, err
	}
	return map[string]any{"question": row, "navigation": navigation}, nil
}

func (s *Service) ProfileCenter(ctx context.Context, caller Caller) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	owned := []Filter{{Column: "user_id", Op: "eq", Value: caller.UserID}}
	enrollments, err := s.many(ctx, caller, Query{
		Table: "course_enrollments", Select: "id,course_id,status,source,granted_at,expires_at,note,courses(id,title,description,cover_image_url,price,is_free)",
		Filters: owned, Orders: []Order{{Column: "granted_at", Desc: true}}, Limit: 100,
	})
	if err != nil {
		return nil, err
	}
	purchases, err := s.many(ctx, caller, Query{
		Table: "course_purchase_requests", Select: "id,course_id,course_title,course_price,status,admin_note,contacted_at,paid_at,created_at",
		Filters: owned, Orders: []Order{{Column: "created_at", Desc: true}}, Limit: 100,
	})
	if err != nil {
		return nil, err
	}
	progress, err := s.many(ctx, caller, Query{
		Table: "lesson_progress", Select: "id,course_id,lesson_id,current_seconds,duration_seconds,progress_percent,is_completed,last_watched_at,lessons(id,title),courses(id,title)",
		Filters: owned, Orders: []Order{{Column: "last_watched_at", Desc: true}}, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	favorites, err := s.many(ctx, caller, Query{
		Table: "interview_question_favorites", Select: "id,created_at,interview_question(id,title,content,difficulty,collection_id)",
		Filters: owned, Orders: []Order{{Column: "created_at", Desc: true}}, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	submissions, err := s.many(ctx, caller, Query{
		Table: "interview_question_submissions", Select: "id,title,content,tags,status,admin_note,created_at,updated_at",
		Filters: owned, Orders: []Order{{Column: "created_at", Desc: true}}, Limit: 100,
	})
	if err != nil {
		return nil, err
	}
	resumes, err := s.many(ctx, caller, Query{
		Table: "resume_optimization_records", Select: "id,target_role,score,positioning,rewritten_summary,keywords,resume_html,created_at",
		Filters: owned, Orders: []Order{{Column: "created_at", Desc: true}}, Limit: 50,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"enrollments": enrollments.Rows, "purchaseRequests": purchases.Rows, "progress": progress.Rows,
		"favorites": favorites.Rows, "submissions": submissions.Rows, "resumeRecords": resumes.Rows,
	}, nil
}

func (s *Service) AdminQuestions(ctx context.Context, caller Caller, collectionID, keyword string) (Result, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return Result{}, err
	}
	filters := []Filter{}
	if collectionID != "" {
		if !IsUUID(collectionID) {
			return Result{}, invalid("题集标识不合法")
		}
		filters = append(filters, Filter{Column: "collection_id", Op: "eq", Value: collectionID})
	}
	if strings.TrimSpace(keyword) != "" {
		filters = append(filters, Filter{Column: "title", Op: "ilike", Value: keyword})
	}
	return s.many(ctx, caller, Query{
		Table: "interview_question", Select: "id,title,content,difficulty,is_vip,vip_level_required,collection_id,sort,created_at,updated_at",
		Filters: filters, Orders: []Order{{Column: "created_at", Desc: true}}, Limit: 200,
	})
}

func (s *Service) SaveQuestion(ctx context.Context, caller Caller, id string, input map[string]any, create bool) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	payload := map[string]any{}
	for _, key := range []string{"title", "content", "difficulty"} {
		if _, ok := input[key]; ok {
			payload[key] = nullable(stringField(input, key))
		}
	}
	if _, ok := input["collectionId"]; ok {
		value := stringField(input, "collectionId")
		if value != "" && !IsUUID(value) {
			return nil, invalid("题集标识不合法")
		}
		payload["collection_id"] = nullable(value)
	}
	if _, ok := input["isVip"]; ok {
		payload["is_vip"] = boolField(input, "isVip")
	}
	if _, ok := input["sort"]; ok {
		payload["sort"] = int(nonNegative(input["sort"]))
	}
	if _, ok := input["vipLevelRequired"]; ok {
		payload["vip_level_required"] = int(nonNegative(input["vipLevelRequired"]))
	}
	if create {
		if stringField(input, "title") == "" {
			return nil, invalid("题目标题不能为空")
		}
		payload["title"] = stringField(input, "title")
		result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
			Table: "interview_question", Select: "id,title,content,difficulty,is_vip,collection_id,sort,created_at",
		}, payload)
		if err != nil {
			return nil, err
		}
		return result.Rows[0], nil
	}
	if !IsUUID(id) {
		return nil, invalid("题目标识不合法")
	}
	result, err := s.source.Write(ctx, caller, http.MethodPatch, Query{
		Table: "interview_question", Select: "id,title,content,difficulty,is_vip,collection_id,sort,updated_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
	}, payload)
	if err != nil {
		return nil, err
	}
	return result.Rows[0], nil
}

func (s *Service) DeleteQuestion(ctx context.Context, caller Caller, id string) error {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return err
	}
	if !IsUUID(id) {
		return invalid("题目标识不合法")
	}
	_, err := s.source.Write(ctx, caller, http.MethodDelete, Query{
		Table: "interview_question", Select: "id", Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
	}, map[string]any{})
	if apiErr, ok := err.(*Error); ok && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}

func (s *Service) CreateSubmission(ctx context.Context, caller Caller, input map[string]any) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	title := stringField(input, "title")
	content := stringField(input, "content")
	if title == "" || content == "" {
		return nil, invalid("标题和内容不能为空")
	}
	payload := map[string]any{
		"user_id": caller.UserID, "title": title, "content": content, "status": "pending",
		"contact": nullable(stringField(input, "contact")), "difficulty": nullable(stringField(input, "difficulty")),
		"source": nullable(stringField(input, "source")),
	}
	if collectionID := stringField(input, "collectionId"); collectionID != "" {
		if !IsUUID(collectionID) {
			return nil, invalid("题集标识不合法")
		}
		payload["collection_id"] = collectionID
	}
	if tags, ok := input["tags"].([]any); ok {
		clean := []string{}
		for _, tag := range tags {
			text, _ := tag.(string)
			text = strings.TrimSpace(text)
			if text != "" && len(clean) < 12 {
				clean = append(clean, text)
			}
		}
		payload["tags"] = clean
	}
	for _, key := range []string{"attachment_path", "attachment_name", "attachment_mime_type"} {
		if value := stringField(input, key); value != "" {
			if key == "attachment_path" && !strings.HasPrefix(value, caller.UserID+"/") {
				return nil, forbidden("附件路径不属于当前用户")
			}
			payload[key] = value
		}
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table: "interview_question_submissions", Select: "id,title,status,created_at",
	}, payload)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权投稿")
	}
	return result.Rows[0], nil
}

func (s *Service) SaveResumeRecord(ctx context.Context, caller Caller, input map[string]any) error {
	if err := s.requireUser(caller); err != nil {
		return err
	}
	_, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table: "resume_optimization_records", Select: "id",
	}, map[string]any{
		"user_id":           caller.UserID,
		"target_role":       stringField(input, "targetRole"),
		"experience_level":  stringField(input, "experienceLevel"),
		"job_description":   nullable(stringField(input, "jobDescription")),
		"resume_excerpt":    stringField(input, "resumeExcerpt"),
		"score":             input["score"],
		"positioning":       input["positioning"],
		"rewritten_summary": input["rewrittenSummary"],
		"optimized_bullets": input["optimizedBullets"],
		"keywords":          input["keywords"],
		"action_plan":       input["actionPlan"],
		"interview_stories": input["interviewStories"],
		"resume_html":       input["resumeHtml"],
		"meta":              input["meta"],
	})
	return err
}

func (s *Service) ReserveFaucet(ctx context.Context, caller Caller, wallet, network, ip string) (map[string]any, error) {
	wallet = strings.TrimSpace(wallet)
	if wallet == "" || len(wallet) > 80 || strings.ContainsAny(wallet, ",()*'\"") {
		return nil, invalid("无效的钱包地址")
	}
	if network != "sepolia" && network != "bsc-testnet" {
		return nil, invalid("不支持的区块链网络")
	}
	since := time.Now().Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent, err := s.many(ctx, caller, Query{
		Table: "faucet_claims", Select: "id,created_at",
		Filters: []Filter{{Column: "wallet_address", Op: "eq", Value: wallet}, {Column: "created_at", Op: "gte", Value: since}},
		Limit:   5,
	})
	if err != nil {
		return nil, err
	}
	if len(recent.Rows) > 0 {
		return nil, NewError(http.StatusTooManyRequests, "COOLDOWN", "该钱包地址近期已领取过测试币")
	}
	dayAgo := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	daily, err := s.many(ctx, caller, Query{
		Table: "faucet_claims", Select: "amount",
		Filters: []Filter{{Column: "created_at", Op: "gte", Value: dayAgo}}, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	total := 0.0
	for _, row := range daily.Rows {
		amount, _ := numberValue(row["amount"])
		total += amount
	}
	if total+0.1 > 2 {
		return nil, NewError(http.StatusTooManyRequests, "DAILY_LIMIT", "今日测试币发放已达上限，请明天再来")
	}
	txHash := "pending_" + network + "_" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "")
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{Table: "faucet_claims", Select: "id"}, map[string]any{
		"wallet_address": wallet, "ip_address": ip, "amount": 0.1, "tx_hash": txHash,
	})
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权创建领取记录")
	}
	return result.Rows[0], nil
}

func (s *Service) FinishFaucet(ctx context.Context, caller Caller, id, txHash string, remove bool) error {
	if !IsUUID(id) {
		return invalid("领取记录不合法")
	}
	if remove {
		_, err := s.source.Write(ctx, caller, http.MethodDelete, Query{
			Table: "faucet_claims", Select: "id", Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
		}, map[string]any{})
		return err
	}
	if txHash == "" || strings.ContainsAny(txHash, ",()*'\"") {
		return invalid("交易哈希不合法")
	}
	_, err := s.source.Write(ctx, caller, http.MethodPatch, Query{
		Table: "faucet_claims", Select: "id,tx_hash", Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
	}, map[string]any{"tx_hash": txHash})
	return err
}
