package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/service"
)

type ChunkUploadHandler struct {
	uploads *service.ChunkUploadService
}

func NewChunkUploadHandler(uploads *service.ChunkUploadService) *ChunkUploadHandler {
	return &ChunkUploadHandler{uploads: uploads}
}

func (h *ChunkUploadHandler) Init(c *gin.Context) {
	var req service.InitChunkUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	result, err := h.uploads.Init(c.Request.Context(), req)
	if err != nil {
		writeChunkUploadError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

func (h *ChunkUploadHandler) UploadChunk(c *gin.Context) {
	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid upload id"})
		return
	}

	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "invalid chunk index"})
		return
	}

	var reader = c.Request.Body
	if file, formErr := c.FormFile("chunk"); formErr == nil && file != nil {
		f, openErr := file.Open()
		if openErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": openErr.Error()})
			return
		}
		defer f.Close()
		reader = f
	}

	if err := h.uploads.UploadChunk(c.Request.Context(), uploadID, index, reader); err != nil {
		writeChunkUploadError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "chunkIndex": index})
}

func (h *ChunkUploadHandler) Status(c *gin.Context) {
	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid upload id"})
		return
	}

	result, err := h.uploads.Status(c.Request.Context(), uploadID)
	if err != nil {
		writeChunkUploadError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

func (h *ChunkUploadHandler) Complete(c *gin.Context) {
	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ID", "message": "invalid upload id"})
		return
	}

	videoID, err := h.uploads.Complete(c.Request.Context(), uploadID)
	if err != nil {
		writeChunkUploadError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"videoId": videoID})
}

func writeChunkUploadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrUploadSessionNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "NOT_FOUND", "message": "upload session not found"})
	case errors.Is(err, service.ErrUploadSessionExpired):
		c.JSON(http.StatusGone, gin.H{"code": "UPLOAD_EXPIRED", "message": "upload session expired"})
	case errors.Is(err, service.ErrInvalidChunkIndex):
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_CHUNK", "message": "invalid chunk index"})
	case errors.Is(err, service.ErrInvalidChunkSize):
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_CHUNK", "message": err.Error()})
	case errors.Is(err, service.ErrUploadIncomplete):
		c.JSON(http.StatusBadRequest, gin.H{"code": "UPLOAD_INCOMPLETE", "message": "not all chunks uploaded"})
	case errors.Is(err, service.ErrInvalidFileType):
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_FILE_TYPE", "message": "only mp4 files are supported"})
	case errors.Is(err, service.ErrFileTooLarge):
		c.JSON(http.StatusBadRequest, gin.H{"code": "FILE_TOO_LARGE", "message": "file exceeds maximum upload size"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "INTERNAL_ERROR", "message": err.Error()})
	}
}
