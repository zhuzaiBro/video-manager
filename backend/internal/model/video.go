package model

import (
	"time"

	"github.com/google/uuid"
)

const (
	VideoStatusWaiting    = "waiting"
	VideoStatusProcessing = "processing"
	VideoStatusReady      = "ready"
	VideoStatusFailed     = "failed"
)

type Video struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Title        string    `gorm:"not null" json:"title"`
	Description  *string   `json:"description,omitempty"`
	SourceFile   string    `gorm:"column:source_file;not null" json:"-"`
	COSPrefix    *string   `gorm:"column:cos_prefix" json:"cosPrefix,omitempty"`
	M3U8Path     *string   `gorm:"column:m3u8_path" json:"m3u8Path,omitempty"`
	Duration     *int      `json:"duration,omitempty"`
	Width        *int      `json:"width,omitempty"`
	Height       *int      `json:"height,omitempty"`
	FPS          *float64  `gorm:"column:fps" json:"fps,omitempty"`
	SegmentCount *int      `gorm:"column:segment_count" json:"segmentCount,omitempty"`
	Status       string    `gorm:"not null;default:waiting" json:"status"`
	ErrorMessage *string   `gorm:"column:error_message" json:"errorMessage,omitempty"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

func (Video) TableName() string {
	return "videos"
}
