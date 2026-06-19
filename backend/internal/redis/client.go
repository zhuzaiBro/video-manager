package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zood/video-manager/internal/config"
)

type Client struct {
	rdb *redis.Client
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		rdb: redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		}),
	}
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func usageKey(date, userID string) string {
	return fmt.Sprintf("video:usage:%s:%s", date, userID)
}

func onlineKey(userID string) string {
	return fmt.Sprintf("video:online:%s", userID)
}

func (c *Client) GetWatchSeconds(ctx context.Context, userID string, date time.Time) (int, error) {
	val, err := c.rdb.Get(ctx, usageKey(date.Format("2006-01-02"), userID)).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(val)
}

func (c *Client) AddWatchSeconds(ctx context.Context, userID string, date time.Time, seconds int) (int, error) {
	key := usageKey(date.Format("2006-01-02"), userID)
	pipe := c.rdb.Pipeline()
	incr := pipe.IncrBy(ctx, key, int64(seconds))
	pipe.Expire(ctx, key, config.UsageKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int(incr.Val()), nil
}

func (c *Client) SetWatchSeconds(ctx context.Context, userID string, date time.Time, seconds int) error {
	key := usageKey(date.Format("2006-01-02"), userID)
	pipe := c.rdb.Pipeline()
	pipe.Set(ctx, key, seconds, config.UsageKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return nil
}

func (c *Client) RefreshOnline(ctx context.Context, userID string) error {
	return c.rdb.Set(ctx, onlineKey(userID), "1", config.OnlineDeviceTTL).Err()
}

func segmentKey(userID, videoID, segment string) string {
	return fmt.Sprintf("video:seg:%s:%s:%s", userID, videoID, segment)
}

// TryMarkSegmentWatched 同一切片 6 秒内只计一次，避免 HLS 重试重复统计。
func (c *Client) TryMarkSegmentWatched(ctx context.Context, userID, videoID, segment string) (bool, error) {
	return c.rdb.SetNX(ctx, segmentKey(userID, videoID, segment), "1", config.SegmentDurationSeconds*time.Second).Result()
}
