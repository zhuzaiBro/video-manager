package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	cosclient "github.com/zood/video-manager/internal/cos"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/model"
	redisclient "github.com/zood/video-manager/internal/redis"
	"github.com/zood/video-manager/internal/repository"
	"gorm.io/gorm"
)

type PlaybackService struct {
	cfg    *config.Config
	videos *repository.VideoRepository
	usage  *repository.UsageRepository
	redis  *redisclient.Client
	cos    *cosclient.Client
}

func NewPlaybackService(
	cfg *config.Config,
	videos *repository.VideoRepository,
	usage *repository.UsageRepository,
	redis *redisclient.Client,
	cos *cosclient.Client,
) *PlaybackService {
	return &PlaybackService{
		cfg:    cfg,
		videos: videos,
		usage:  usage,
		redis:  redis,
		cos:    cos,
	}
}

type PlayResult struct {
	PlayURL  string `json:"playUrl"`
	ExpireAt int64  `json:"expireAt"`
}

func (s *PlaybackService) Play(ctx context.Context, userID string, videoID uuid.UUID, accessToken string) (*PlayResult, error) {
	video, err := s.videos.FindByID(videoID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoNotFound
		}
		return nil, err
	}
	if video.Status != model.VideoStatusReady {
		return nil, ErrVideoNotReady
	}
	if video.M3U8Path == nil || *video.M3U8Path == "" {
		return nil, ErrVideoNotReady
	}

	today := time.Now()
	watchSeconds, err := s.redis.GetWatchSeconds(ctx, userID, today)
	if err != nil {
		return nil, err
	}
	if watchSeconds >= config.DailyWatchLimitSeconds {
		return nil, ErrWatchLimitExceeded
	}

	_ = s.redis.RefreshOnline(ctx, userID)
	_ = s.logAccess(ctx, userID, videoID, "play_start", 0, "")

	expire := time.Duration(s.cfg.TokenExpireSec) * time.Second
	expireAt := time.Now().Add(expire).Unix()

	playURL, err := s.buildCOSPlayURL(ctx, videoID, accessToken, expire)
	if err != nil {
		return nil, err
	}

	return &PlayResult{
		PlayURL:  playURL,
		ExpireAt: expireAt,
	}, nil
}

// buildCOSPlayURL 拉取原始 m3u8，改写切片为「API 统计 + COS 预签名」地址，上传 _play.m3u8 后返回其预签名 URL。
func (s *PlaybackService) buildCOSPlayURL(ctx context.Context, videoID uuid.UUID, accessToken string, expire time.Duration) (string, error) {
	if s.cfg.COSCustomDomain == "" {
		return s.apiMediaURL(videoID, "index.m3u8", accessToken), nil
	}

	sourceKey := path.Join("videos", videoID.String(), "index.m3u8")
	resp, err := s.cos.GetObject(ctx, sourceKey)
	if err != nil {
		return "", fmt.Errorf("get m3u8 from cos: %w", err)
	}
	defer resp.Body.Close()

	rewritten, err := rewriteM3U8(resp.Body, func(name string) string {
		return s.trackURL(videoID, name, accessToken)
	})
	if err != nil {
		return "", err
	}

	playKey := s.cos.PlayListKey(videoID)
	if err := s.cos.PutObject(ctx, playKey, rewritten, "application/vnd.apple.mpegurl"); err != nil {
		return "", fmt.Errorf("upload play m3u8: %w", err)
	}

	return s.cos.PresignedGetURL(ctx, playKey, expire)
}

type MediaObject struct {
	Body        io.ReadCloser
	ContentType string
	RedirectURL string
}

func (s *PlaybackService) ServeMedia(ctx context.Context, userID string, videoID uuid.UUID, filename, accessToken, ip string) (*MediaObject, error) {
	if err := validateMediaFilename(filename); err != nil {
		return nil, err
	}

	video, err := s.videos.FindByID(videoID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoNotFound
		}
		return nil, err
	}
	if video.Status != model.VideoStatusReady {
		return nil, ErrVideoNotReady
	}

	if strings.HasSuffix(strings.ToLower(filename), ".m4s") {
		if err := s.recordSegmentWatch(ctx, userID, videoID, filename, ip); err != nil {
			return nil, err
		}
	}

	expire := time.Duration(s.cfg.TokenExpireSec) * time.Second
	cosKey := path.Join("videos", videoID.String(), filename)
	presigned, err := s.cos.PresignedGetURL(ctx, cosKey, expire)
	if err != nil {
		return nil, fmt.Errorf("presign cos url: %w", err)
	}

	return &MediaObject{RedirectURL: presigned}, nil
}

// trackURL：切片先走 API（统计观看）再 302 到 COS 预签名地址。
func (s *PlaybackService) trackURL(videoID uuid.UUID, filename, token string) string {
	return s.apiMediaURL(videoID, filename, token)
}

func (s *PlaybackService) apiMediaURL(videoID uuid.UUID, filename, token string) string {
	return fmt.Sprintf("%s/api/videos/%s/media/%s?token=%s",
		strings.TrimRight(s.apiBase(), "/"),
		videoID,
		filename,
		url.QueryEscape(token),
	)
}

func (s *PlaybackService) apiBase() string {
	if s.cfg.APIBaseURL != "" {
		return s.cfg.APIBaseURL
	}
	return s.cfg.ProxyBaseURL
}

func validateMediaFilename(filename string) error {
	if filename == "" || strings.Contains(filename, "..") || strings.Contains(filename, "/") {
		return fmt.Errorf("invalid media filename")
	}
	return nil
}

func rewriteM3U8(r io.Reader, buildURL func(string) string) ([]byte, error) {
	var out bytes.Buffer
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
			out.WriteString(line + "\n")
		case strings.HasPrefix(trimmed, "#"):
			if strings.Contains(trimmed, `URI="`) {
				out.WriteString(rewriteMapURI(trimmed, buildURL) + "\n")
			} else {
				out.WriteString(line + "\n")
			}
		default:
			out.WriteString(buildURL(trimmed) + "\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func rewriteMapURI(line string, buildURL func(string) string) string {
	start := strings.Index(line, `URI="`)
	if start == -1 {
		return line
	}
	start += len(`URI="`)
	end := strings.Index(line[start:], `"`)
	if end == -1 {
		return line
	}
	filename := line[start : start+end]
	return line[:start] + buildURL(filename) + line[start+end:]
}

func (s *PlaybackService) ReportSegment(ctx context.Context, userID string, videoID uuid.UUID, segmentName, ip string) error {
	return s.recordSegmentWatch(ctx, userID, videoID, segmentName, ip)
}

func (s *PlaybackService) recordSegmentWatch(ctx context.Context, userID string, videoID uuid.UUID, segmentName, ip string) error {
	first, err := s.redis.TryMarkSegmentWatched(ctx, userID, videoID.String(), segmentName)
	if err != nil {
		return err
	}
	if !first {
		return nil
	}

	today := time.Now()
	watchSeconds, err := s.redis.GetWatchSeconds(ctx, userID, today)
	if err != nil {
		return err
	}
	if watchSeconds >= config.DailyWatchLimitSeconds {
		return ErrWatchLimitExceeded
	}

	newTotal, err := s.redis.AddWatchSeconds(ctx, userID, today, config.SegmentDurationSeconds)
	if err != nil {
		return err
	}

	if err := s.logAccess(ctx, userID, videoID, segmentName, config.SegmentDurationSeconds, ip); err != nil {
		return err
	}

	return s.usage.UpsertUsage(userID, today, newTotal)
}

func (s *PlaybackService) logAccess(ctx context.Context, userID string, videoID uuid.UUID, segmentName string, watchSeconds int, ip string) error {
	seg := segmentName
	log := &model.VideoAccessLog{
		UserID:      userID,
		VideoID:     videoID,
		SegmentName: &seg,
	}
	if watchSeconds > 0 {
		log.WatchSeconds = &watchSeconds
	}
	if ip != "" {
		log.IP = &ip
	}
	return s.usage.CreateAccessLog(log)
}

type WatchStatsResult struct {
	Summary    []repository.UserWatchSummary `json:"summary"`
	RecentLogs []model.VideoAccessLog        `json:"recentLogs"`
}

func (s *PlaybackService) WatchStats(ctx context.Context, videoID uuid.UUID) (*WatchStatsResult, error) {
	if _, err := s.videos.FindByID(videoID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoNotFound
		}
		return nil, err
	}

	summary, err := s.usage.SummarizeByVideo(videoID)
	if err != nil {
		return nil, err
	}
	logs, err := s.usage.ListAccessLogsByVideo(videoID, 50)
	if err != nil {
		return nil, err
	}

	return &WatchStatsResult{
		Summary:    summary,
		RecentLogs: logs,
	}, nil
}
