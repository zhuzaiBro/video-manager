package model

import (
	"time"

	"github.com/google/uuid"
)

type UserVideoUsage struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       string    `gorm:"column:user_id;not null" json:"userId"`
	UsageDate    time.Time `gorm:"column:usage_date;type:date;not null" json:"usageDate"`
	WatchSeconds int       `gorm:"column:watch_seconds;not null;default:0" json:"watchSeconds"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

func (UserVideoUsage) TableName() string {
	return "user_video_usage"
}

type VideoAccessLog struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       string    `gorm:"column:user_id;not null" json:"userId"`
	VideoID      uuid.UUID `gorm:"column:video_id;type:uuid;not null" json:"videoId"`
	SegmentName  *string   `gorm:"column:segment_name" json:"segmentName,omitempty"`
	WatchSeconds *int      `gorm:"column:watch_seconds" json:"watchSeconds,omitempty"`
	IP           *string   `gorm:"type:inet" json:"ip,omitempty"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`
}

func (VideoAccessLog) TableName() string {
	return "video_access_logs"
}
