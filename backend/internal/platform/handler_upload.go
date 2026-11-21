package platform

import (
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var uploadRules = map[string]struct {
	max  int64
	mime map[string]string
}{
	"post-banners": {5 << 20, map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}},
	"interview-submission-attachments": {20 << 20, map[string]string{
		"application/pdf":    "pdf",
		"application/msword": "doc",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
		"application/vnd.ms-excel": "xls",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
	}},
	"lesson-courseware": {50 << 20, map[string]string{
		"application/pdf": "pdf", "text/plain": "txt", "application/zip": "zip",
		"application/msword": "doc",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
		"application/vnd.ms-powerpoint":                                             "ppt",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
		"application/vnd.ms-excel":                                                  "xls",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	}},
}

func (h *Handler) upload(c *gin.Context, caller Caller) {
	if h.blob == nil {
		writeErr(c, NewError(http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "对象存储未配置，旧的 Supabase 文件也无法从本地库取回"))
		return
	}
	rule, ok := uploadRules[c.PostForm("bucket")]
	if !ok {
		writeErr(c, invalid("不支持的上传位置"))
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeErr(c, invalid("缺少文件"))
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > rule.max {
		writeErr(c, invalid("文件大小超出限制"))
		return
	}
	ext, ok := rule.mime[header.Header.Get("Content-Type")]
	if !ok {
		writeErr(c, invalid("文件类型不被允许"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(file, rule.max+1))
	if err != nil || int64(len(body)) > rule.max {
		writeErr(c, invalid("文件大小超出限制"))
		return
	}
	key := path.Join("platform", c.PostForm("bucket"), caller.UserID, uuid.NewString()+"."+ext)
	if err := h.blob.PutObject(c.Request.Context(), key, body, header.Header.Get("Content-Type")); err != nil {
		writeErr(c, NewError(http.StatusBadGateway, "STORAGE_UNAVAILABLE", "对象存储写入失败"))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"path":                 key,
		"publicUrl":            h.blob.PublicObjectURL(key),
		"fileName":             strings.TrimSpace(header.Filename),
		"missingLegacyObjects": true,
	})
}
