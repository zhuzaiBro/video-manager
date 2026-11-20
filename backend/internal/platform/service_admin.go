package platform

import (
	"context"
	"net/http"
)

func (s *Service) CreateCourse(ctx context.Context, caller Caller, input map[string]any) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	title := stringField(input, "title")
	if title == "" {
		return nil, invalid("课程标题不能为空")
	}
	count, err := s.many(ctx, caller, Query{Table: "courses", Select: "id", Limit: 200, Count: true})
	if err != nil {
		return nil, err
	}
	total := len(count.Rows)
	if count.Count != nil {
		total = *count.Count
	}
	result, err := s.source.Write(ctx, caller, http.MethodPost, Query{
		Table: "courses", Select: "id,title,description,sort_order,status,created_at",
	}, map[string]any{
		"title": title, "description": nullable(stringField(input, "description")),
		"sort_order": total, "created_by": caller.UserID, "status": "draft",
	})
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, forbidden("无权创建课程")
	}
	return result.Rows[0], nil
}

func (s *Service) SaveChapter(ctx context.Context, caller Caller, id string, input map[string]any, create bool) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if _, ok := input["title"]; ok {
		title := stringField(input, "title")
		if title == "" {
			return nil, invalid("章节标题不能为空")
		}
		payload["title"] = title
	}
	if _, ok := input["description"]; ok {
		payload["description"] = nullable(stringField(input, "description"))
	}
	if _, ok := input["courseId"]; ok {
		courseID := stringField(input, "courseId")
		if !IsUUID(courseID) {
			return nil, invalid("课程标识不合法")
		}
		payload["course_id"] = courseID
	}
	if _, ok := input["sortOrder"]; ok {
		payload["sort_order"] = int(nonNegative(input["sortOrder"]))
	}
	if create {
		if payload["course_id"] == nil || payload["title"] == nil {
			return nil, invalid("章节缺少课程或标题")
		}
		result, err := s.source.Write(ctx, caller, http.MethodPost, Query{Table: "chapters", Select: "id,course_id,title,description,sort_order"}, payload)
		if err != nil {
			return nil, err
		}
		return firstRow(result)
	}
	if !IsUUID(id) {
		return nil, invalid("章节标识不合法")
	}
	result, err := s.source.Write(ctx, caller, http.MethodPatch, Query{
		Table: "chapters", Select: "id,course_id,title,description,sort_order", Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
	}, payload)
	if err != nil {
		return nil, err
	}
	return firstRow(result)
}

func (s *Service) DeleteChapter(ctx context.Context, caller Caller, id string) error {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return err
	}
	if !IsUUID(id) {
		return invalid("章节标识不合法")
	}
	_, err := s.source.Write(ctx, caller, http.MethodDelete, Query{
		Table: "chapters", Select: "id", Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
	}, map[string]any{})
	return ignoreNotFound(err)
}

func (s *Service) SaveLesson(ctx context.Context, caller Caller, id string, input map[string]any, create bool) (map[string]any, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return nil, err
	}
	payload := map[string]any{}
	copyString(payload, input, "title", "title")
	copyString(payload, input, "description", "description")
	copyString(payload, input, "coursewareName", "courseware_name")
	copyString(payload, input, "coursewareUrl", "courseware_url")
	copyString(payload, input, "contentHtml", "content_html")
	copyString(payload, input, "contentMarkdown", "content_markdown")
	copyString(payload, input, "videoId", "video_id")
	copyString(payload, input, "videoUrl", "video_url")
	if _, ok := input["chapterId"]; ok {
		chapterID := stringField(input, "chapterId")
		if !IsUUID(chapterID) {
			return nil, invalid("章节标识不合法")
		}
		payload["chapter_id"] = chapterID
	}
	if _, ok := input["isFree"]; ok {
		payload["is_free"] = boolField(input, "isFree")
	}
	if _, ok := input["sortOrder"]; ok {
		payload["sort_order"] = int(nonNegative(input["sortOrder"]))
	}
	if _, ok := input["duration"]; ok {
		payload["duration"] = int(nonNegative(input["duration"]))
	}
	if create {
		if payload["chapter_id"] == nil || stringField(input, "title") == "" {
			return nil, invalid("课时缺少章节或标题")
		}
		payload["title"] = stringField(input, "title")
		result, err := s.source.Write(ctx, caller, http.MethodPost, Query{Table: "lessons", Select: "id,chapter_id,title,video_id,sort_order"}, payload)
		if err != nil {
			return nil, err
		}
		return firstRow(result)
	}
	if !IsUUID(id) {
		return nil, invalid("课时标识不合法")
	}
	result, err := s.source.Write(ctx, caller, http.MethodPatch, Query{
		Table: "lessons", Select: "id,chapter_id,title,video_id,video_url,sort_order,updated_at",
		Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
	}, payload)
	if err != nil {
		return nil, err
	}
	return firstRow(result)
}

func (s *Service) DeleteLesson(ctx context.Context, caller Caller, id string) error {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return err
	}
	if !IsUUID(id) {
		return invalid("课时标识不合法")
	}
	_, err := s.source.Write(ctx, caller, http.MethodDelete, Query{
		Table: "lessons", Select: "id", Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
	}, map[string]any{})
	return ignoreNotFound(err)
}

func (s *Service) Reorder(ctx context.Context, caller Caller, table string, ids []string) error {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return err
	}
	if table != "courses" && table != "chapters" && table != "lessons" {
		return invalid("不支持的排序资源")
	}
	if len(ids) == 0 || len(ids) > 200 {
		return invalid("排序列表不合法")
	}
	for index, id := range ids {
		if !IsUUID(id) {
			return invalid("排序标识不合法")
		}
		if _, err := s.source.Write(ctx, caller, http.MethodPatch, Query{
			Table: table, Select: "id,sort_order", Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
		}, map[string]any{"sort_order": index}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListVideos(ctx context.Context, caller Caller) (Result, error) {
	if err := s.requireAdmin(ctx, caller); err != nil {
		return Result{}, err
	}
	return s.many(ctx, caller, Query{
		Table: "videos", Select: "id,title,status,duration,created_at,updated_at",
		Orders: []Order{{Column: "created_at", Desc: true}}, Limit: 200,
	})
}

func copyString(dest, src map[string]any, from, to string) {
	if _, ok := src[from]; ok {
		dest[to] = nullable(stringField(src, from))
	}
}

func firstRow(result Result) (map[string]any, error) {
	if len(result.Rows) == 0 {
		return nil, forbidden("无权写入")
	}
	return result.Rows[0], nil
}

func ignoreNotFound(err error) error {
	if apiErr, ok := err.(*Error); ok && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}
