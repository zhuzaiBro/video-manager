package platform

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Service) questionNavigation(ctx context.Context, caller Caller, question map[string]any) (map[string]any, error) {
	empty := map[string]any{"index": -1, "count": 0, "previous": nil, "next": nil, "questions": []any{}}
	collectionID := stringField(question, "collection_id")
	if !IsUUID(collectionID) {
		return empty, nil
	}
	rows, err := s.many(ctx, caller, Query{
		Table:   "interview_question",
		Select:  "id,title,sort,created_at",
		Filters: []Filter{{Column: "collection_id", Op: "eq", Value: collectionID}},
		Orders:  []Order{{Column: "sort"}, {Column: "created_at"}},
		Limit:   200,
	})
	if err != nil {
		return nil, err
	}
	questions := make([]any, 0, len(rows.Rows))
	index := -1
	currentID := stringField(question, "id")
	for i, row := range rows.Rows {
		questions = append(questions, map[string]any{"id": row["id"], "title": row["title"]})
		if stringField(row, "id") == currentID {
			index = i
		}
	}
	var previous, next any
	if index > 0 {
		previous = questions[index-1]
	}
	if index >= 0 && index+1 < len(questions) {
		next = questions[index+1]
	}
	return map[string]any{
		"index": index, "count": len(questions), "previous": previous, "next": next, "questions": questions,
	}, nil
}

func (s *Service) attachPublishedStats(ctx context.Context, caller Caller, courses []map[string]any) error {
	ids := make([]string, 0, len(courses))
	for _, course := range courses {
		if id := stringField(course, "id"); IsUUID(id) {
			ids = append(ids, id)
		}
		course["lessonCount"] = 0
		course["totalDuration"] = 0
	}
	if len(ids) == 0 {
		return nil
	}
	chapters, err := s.many(ctx, caller, Query{
		Table: "chapters", Select: "id,course_id",
		Filters: []Filter{{Column: "course_id", Op: "in", Values: ids}}, Limit: 200,
	})
	if err != nil {
		return err
	}
	chapterCourse := map[string]string{}
	chapterIDs := make([]string, 0, len(chapters.Rows))
	for _, chapter := range chapters.Rows {
		id := stringField(chapter, "id")
		courseID := stringField(chapter, "course_id")
		if IsUUID(id) && IsUUID(courseID) {
			chapterCourse[id] = courseID
			chapterIDs = append(chapterIDs, id)
		}
	}
	if len(chapterIDs) == 0 {
		return nil
	}
	lessons, err := s.many(ctx, caller, Query{
		Table: "lessons", Select: "id,chapter_id,duration,video_id",
		Filters: []Filter{{Column: "chapter_id", Op: "in", Values: chapterIDs}}, Limit: 200,
	})
	if err != nil {
		return err
	}
	stats := map[string]*courseStat{}
	for _, id := range ids {
		stats[id] = &courseStat{}
	}
	for _, lesson := range lessons.Rows {
		courseID := chapterCourse[stringField(lesson, "chapter_id")]
		stat := stats[courseID]
		if stat == nil {
			continue
		}
		stat.count++
		duration := int(nonNegative(lesson["duration"]))
		if duration == 0 && s.durationLookup != nil {
			if videoID := stringField(lesson, "video_id"); videoID != "" {
				duration = s.durationLookup(ctx, videoID)
			}
		}
		stat.duration += duration
	}
	for _, course := range courses {
		stat := stats[stringField(course, "id")]
		if stat == nil {
			continue
		}
		course["lessonCount"] = stat.count
		course["totalDuration"] = stat.duration
	}
	return nil
}

type courseStat struct {
	count    int
	duration int
}

func (s *Service) ClaimReferral(ctx context.Context, caller Caller, referrerID, collectionID, sourceURL string) (map[string]any, error) {
	if err := s.requireUser(caller); err != nil {
		return nil, err
	}
	if s.privileged == nil {
		return nil, NewError(http.StatusServiceUnavailable, "PRIVILEGED_UNAVAILABLE", "邀请奖励需要在后端配置隔离的管理客户端")
	}
	referrerID = strings.TrimSpace(referrerID)
	if !IsUUID(referrerID) || referrerID == caller.UserID {
		return map[string]any{"awarded": false, "reason": "invalid_referrer"}, nil
	}
	lookup, ok := s.source.(SelfLookup)
	if !ok {
		return nil, NewError(http.StatusServiceUnavailable, "PRIVILEGED_UNAVAILABLE", "无法用当前登录身份确认注册时间")
	}
	self, err := lookup.FetchSelf(ctx, caller)
	if err != nil {
		return nil, err
	}
	if self.ID != "" && self.ID != caller.UserID {
		return nil, forbidden("登录身份不匹配")
	}
	if self.CreatedAt.IsZero() || time.Since(self.CreatedAt) > 7*24*time.Hour {
		return map[string]any{"awarded": false, "reason": "not_new_user"}, nil
	}
	exists, err := s.privileged.ReferralExists(ctx, caller.UserID)
	if err != nil {
		return nil, err
	}
	if exists {
		return map[string]any{"awarded": false, "reason": "already_awarded"}, nil
	}
	if err := s.privileged.UpsertProfiles(ctx, []map[string]any{defaultProfile(self)}, true); err != nil {
		return nil, err
	}
	referrer, found, err := s.privileged.ReadProfile(ctx, referrerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{"awarded": false, "reason": "referrer_not_found"}, nil
	}
	level, _ := numberValue(referrer["vip_level"])
	expires, _ := referrer["vip_expires_at"].(string)
	permanent := level > 0 && expires == ""
	now := time.Now().UTC()
	base := now
	if parsed, err := time.Parse(time.RFC3339, expires); err == nil && parsed.After(now) {
		base = parsed
	}
	var nextExpiry any
	if !permanent {
		nextExpiry = base.Add(24 * time.Hour).Format(time.RFC3339)
	}
	nextLevel := int(level)
	if nextLevel < 1 {
		nextLevel = 1
	}
	var sourceCollection any
	if IsUUID(collectionID) {
		sourceCollection = collectionID
	}
	if len(sourceURL) > 1000 {
		sourceURL = sourceURL[:1000]
	}
	if err := s.privileged.InsertReferral(ctx, map[string]any{
		"referrer_user_id": referrerID, "referred_user_id": caller.UserID,
		"source_collection_id": sourceCollection, "source_url": nullable(sourceURL),
		"reward_days": 1, "reward_vip_level": 1,
	}); err != nil {
		if apiErr, ok := err.(*Error); ok && (apiErr.Code == "23505" || apiErr.Status == http.StatusConflict) {
			return map[string]any{"awarded": false, "reason": "already_awarded"}, nil
		}
		return nil, err
	}
	if err := s.privileged.UpdateVip(ctx, referrerID, nextLevel, nextExpiry); err != nil {
		return nil, err
	}
	return map[string]any{"awarded": true, "reason": "awarded", "vipExpiresAt": nextExpiry}, nil
}

func (s *Service) SyncMissingProfiles(ctx context.Context, caller Caller) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	if s.privileged == nil {
		return nil, NewError(http.StatusServiceUnavailable, "PRIVILEGED_UNAVAILABLE", "补齐 Auth 用户需要在后端配置隔离的管理客户端")
	}
	accounts, err := s.privileged.ListAuthUsers(ctx)
	if err != nil {
		return nil, err
	}
	profiles, err := s.privileged.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, profile := range profiles {
		have[stringField(profile, "id")] = true
	}
	missing := make([]map[string]any, 0)
	for _, account := range accounts {
		if account.ID == "" || have[account.ID] {
			continue
		}
		missing = append(missing, defaultProfile(account))
	}
	if len(missing) > 0 {
		if err := s.privileged.UpsertProfiles(ctx, missing, true); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"createdCount": len(missing),
		"skippedCount": len(accounts) - len(missing),
		"message":      "已按管理员身份补齐缺失的 user_profiles",
	}, nil
}

func (s *Service) GrantMembership(ctx context.Context, caller Caller, targetID string, vipLevel, consultationID int) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	if s.privileged == nil {
		return nil, NewError(http.StatusServiceUnavailable, "PRIVILEGED_UNAVAILABLE", "开通会员需要在后端配置隔离的管理客户端")
	}
	if !IsUUID(targetID) {
		return nil, invalid("缺少用户 ID")
	}
	if vipLevel < 0 || vipLevel > 5 {
		return nil, invalid("会员等级必须是 0-5 的整数")
	}
	profile, found, err := s.privileged.ReadProfile(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if !found {
		account, ok, err := s.privileged.GetAuthUser(ctx, targetID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, notFound("用户不存在")
		}
		if err := s.privileged.UpsertProfiles(ctx, []map[string]any{defaultProfile(account)}, false); err != nil {
			return nil, err
		}
		profile = defaultProfile(account)
	}
	if err := s.privileged.UpdateVip(ctx, targetID, vipLevel, nil); err != nil {
		return nil, err
	}
	if consultationID > 0 && vipLevel > 0 {
		if err := s.privileged.MarkConsultationPaid(ctx, consultationID, targetID, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return nil, err
		}
	}
	profile["vip_level"] = vipLevel
	profile["vip_expires_at"] = nil
	message := "已取消会员"
	if vipLevel > 0 {
		message = "已开通 VIP" + strconv.Itoa(vipLevel)
	}
	return map[string]any{"profile": profile, "message": message}, nil
}

func (s *Service) AdminStudy(ctx context.Context, caller Caller) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	if s.privileged == nil {
		return nil, NewError(http.StatusServiceUnavailable, "PRIVILEGED_UNAVAILABLE", "跨用户学习统计需要在后端配置隔离的管理客户端")
	}
	now := time.Now()
	today := shanghaiDay(now, 0)
	yesterday := shanghaiDay(now, -1)
	profiles, err := s.privileged.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	logs, err := s.privileged.ListWatchLogsSince(ctx, yesterday.start)
	if err != nil {
		return nil, err
	}
	type stat struct {
		today, yesterday int
		recent           string
		videos           map[string]struct{}
	}
	byUser := map[string]*stat{}
	for _, log := range logs {
		userID := stringField(log, "user_id")
		item := byUser[userID]
		if item == nil {
			item = &stat{videos: map[string]struct{}{}}
			byUser[userID] = item
		}
		seconds := int(nonNegative(log["watch_seconds"]))
		created := stringField(log, "created_at")
		if parsed, err := time.Parse(time.RFC3339, created); err == nil && !parsed.Before(today.start) && parsed.Before(today.end) {
			item.today += seconds
		} else {
			item.yesterday += seconds
		}
		if videoID := stringField(log, "video_id"); videoID != "" {
			item.videos[videoID] = struct{}{}
		}
		if created > item.recent {
			item.recent = created
		}
	}
	users := make([]map[string]any, 0, len(profiles))
	for _, profile := range profiles {
		item := byUser[stringField(profile, "id")]
		if item == nil {
			item = &stat{videos: map[string]struct{}{}}
		}
		users = append(users, map[string]any{
			"id": profile["id"], "email": nil, "phone": nil,
			"username": profile["username"], "displayName": profile["display_name"], "avatarUrl": profile["avatar_url"],
			"provider": nil, "providers": []any{}, "role": nil,
			"emailConfirmedAt": nil, "phoneConfirmedAt": nil, "lastSignInAt": nil,
			"isSsoUser": false, "isAnonymous": false, "hasProfile": true,
			"vipLevel": profile["vip_level"], "isAdmin": truthy(profile["is_admin"]), "createdAt": profile["created_at"],
			"todayStudySeconds": item.today, "yesterdayStudySeconds": item.yesterday,
			"totalStudySeconds": item.today + item.yesterday, "uniqueVideoCount": len(item.videos), "recentStudyAt": nullable(item.recent),
		})
	}
	return map[string]any{
		"users": users,
		"summary": map[string]any{
			"userCount": len(users), "missingProfileCount": 0,
		},
		"source": "platform-admin",
		"ranges": map[string]any{
			"today":     map[string]any{"label": today.label, "start": today.start.Format(time.RFC3339), "end": today.end.Format(time.RFC3339)},
			"yesterday": map[string]any{"label": yesterday.label, "start": yesterday.start.Format(time.RFC3339), "end": yesterday.end.Format(time.RFC3339)},
		},
	}, nil
}

func (s *Service) HandleTelegram(ctx context.Context, secret string, body []byte) (int, map[string]any, error) {
	if s.webhookSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(s.webhookSecret)) != 1 {
		return http.StatusUnauthorized, nil, unauthorized("Unauthorized")
	}
	if s.privileged == nil {
		return http.StatusServiceUnavailable, nil, NewError(http.StatusServiceUnavailable, "PRIVILEGED_UNAVAILABLE", "Telegram 映射需要在后端配置隔离的管理客户端")
	}
	var update struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			MessageID int64  `json:"message_id"`
			Text      string `json:"text"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			From *struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			} `json:"from"`
			Reply *struct {
				MessageID int64 `json:"message_id"`
			} `json:"reply_to_message"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &update); err != nil || update.Message == nil || update.Message.From == nil {
		return http.StatusOK, map[string]any{"ok": true}, nil
	}
	message := update.Message
	fields := strings.Fields(strings.TrimSpace(message.Text))
	if len(fields) == 2 && fields[0] == "/start" && s.setupSecret != "" && subtle.ConstantTimeCompare([]byte(fields[1]), []byte(s.setupSecret)) == 1 {
		if err := s.privileged.UpsertTelegramAdmin(ctx, message.Chat.ID, message.From.ID, message.From.Username); err != nil {
			return http.StatusBadGateway, nil, err
		}
		s.replyTelegram(ctx, message.Chat.ID, "绑定成功。之后网站访客发来的消息会出现在这里，直接回复对应消息即可。")
		return http.StatusOK, map[string]any{"ok": true}, nil
	}
	active, err := s.privileged.TelegramAdminActive(ctx, message.Chat.ID, message.From.ID)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if !active {
		s.replyTelegram(ctx, message.Chat.ID, "当前账号未绑定为网站客服。")
		return http.StatusOK, map[string]any{"ok": true}, nil
	}
	if message.Reply == nil {
		s.replyTelegram(ctx, message.Chat.ID, "请使用 Telegram 的“回复”功能，回复一条网站访客消息。")
		return http.StatusOK, map[string]any{"ok": true}, nil
	}
	seen, err := s.privileged.TelegramUpdateSeen(ctx, update.UpdateID)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if seen {
		return http.StatusOK, map[string]any{"ok": true}, nil
	}
	sessionID, ok, err := s.privileged.TelegramReplySession(ctx, message.Chat.ID, message.Reply.MessageID)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if !ok || !IsUUID(sessionID) {
		s.replyTelegram(ctx, message.Chat.ID, "找不到对应的网站会话，请回复较新的访客消息。")
		return http.StatusOK, map[string]any{"ok": true}, nil
	}
	content := strings.TrimSpace(message.Text)
	if len(content) > 2000 {
		content = content[:2000]
	}
	siteID, err := s.privileged.InsertAdminMessage(ctx, sessionID, content)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if err := s.privileged.InsertTelegramLink(ctx, map[string]any{
		"site_message_id": siteID, "session_id": sessionID, "telegram_chat_id": message.Chat.ID,
		"telegram_message_id": message.MessageID, "telegram_update_id": update.UpdateID, "direction": "from_telegram",
	}); err != nil {
		return http.StatusBadGateway, nil, err
	}
	return http.StatusOK, map[string]any{"ok": true}, nil
}

func (s *Service) forwardUserMessage(ctx context.Context, siteMessageID, sessionID, content, pageURL string, caller Caller) int {
	if s.privileged == nil || s.telegram == nil {
		return 0
	}
	chats, err := s.privileged.ListActiveTelegramAdmins(ctx)
	if err != nil {
		return 0
	}
	visitor := "匿名访客"
	if caller.Email != "" {
		visitor = caller.Email
	}
	text := "网站客服新消息\n访客：" + visitor + "\n会话：" + sessionID
	if pageURL != "" {
		text += "\n页面：" + pageURL
	}
	text += "\n\n" + content + "\n\n请直接回复这条消息。"
	forwarded := 0
	for _, chatID := range chats {
		messageID, err := s.telegram.Send(ctx, chatID, text)
		if err != nil {
			continue
		}
		_ = s.privileged.InsertTelegramLink(ctx, map[string]any{
			"site_message_id": siteMessageID, "session_id": sessionID, "telegram_chat_id": chatID,
			"telegram_message_id": messageID, "direction": "to_telegram",
		})
		forwarded++
	}
	return forwarded
}

func (s *Service) replyTelegram(ctx context.Context, chatID int64, text string) {
	if s.telegram != nil {
		_, _ = s.telegram.Send(ctx, chatID, text)
	}
}

func defaultProfile(account AuthAccount) map[string]any {
	username := "user_" + strings.ReplaceAll(account.ID, "-", "")
	if len(username) > 32 {
		username = username[:32]
	}
	if account.Email != "" {
		local := strings.Split(account.Email, "@")[0]
		local = strings.ToLower(local)
		var b strings.Builder
		for _, r := range local {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			suffix := strings.ReplaceAll(account.ID, "-", "")
			if len(suffix) > 8 {
				suffix = suffix[:8]
			}
			base := b.String()
			if len(base) > 24 {
				base = base[:24]
			}
			username = base + "_" + suffix
		}
	}
	display := username
	if name, _ := account.Metadata["full_name"].(string); strings.TrimSpace(name) != "" {
		display = strings.TrimSpace(name)
	} else if name, _ := account.Metadata["name"].(string); strings.TrimSpace(name) != "" {
		display = strings.TrimSpace(name)
	}
	avatar := "https://api.dicebear.com/7.x/identicon/svg?seed=" + account.ID
	if picture, _ := account.Metadata["avatar_url"].(string); strings.TrimSpace(picture) != "" {
		avatar = strings.TrimSpace(picture)
	}
	return map[string]any{
		"id": account.ID, "username": username, "display_name": display, "avatar_url": avatar,
		"bio": nil, "vip_level": 0, "is_admin": false,
	}
}

type dayWindow struct {
	start, end time.Time
	label      string
}

func shanghaiDay(now time.Time, offset int) dayWindow {
	shanghai := now.UTC().Add(8 * time.Hour)
	year, month, day := shanghai.Date()
	startUTC := time.Date(year, month, day+offset, 0, 0, 0, 0, time.UTC).Add(-8 * time.Hour)
	return dayWindow{start: startUTC, end: startUTC.Add(24 * time.Hour), label: startUTC.Add(8 * time.Hour).Format("2006-01-02")}
}
