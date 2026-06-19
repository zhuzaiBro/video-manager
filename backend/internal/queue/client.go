package queue

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const TaskVideoTranscode = "video:transcode"

type TranscodePayload struct {
	VideoID uuid.UUID `json:"videoId"`
}

type Client struct {
	client    *asynq.Client
	inspector *asynq.Inspector
}

func NewClient(redisAddr, redisPassword string, redisDB int) *Client {
	opt := asynq.RedisClientOpt{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       redisDB,
	}
	return &Client{
		client:    asynq.NewClient(opt),
		inspector: asynq.NewInspector(opt),
	}
}

func (c *Client) Close() error {
	return c.client.Close()
}

func (c *Client) EnqueueTranscode(videoID uuid.UUID) error {
	payload, err := json.Marshal(TranscodePayload{VideoID: videoID})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	task := asynq.NewTask(TaskVideoTranscode, payload)
	_, err = c.client.Enqueue(task, asynq.MaxRetry(3))
	return err
}

func NewServer(redisAddr, redisPassword string, redisDB int) *asynq.Server {
	return asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     redisAddr,
			Password: redisPassword,
			DB:       redisDB,
		},
		asynq.Config{
			Concurrency: 2,
			Queues: map[string]int{
				"default": 10,
			},
		},
	)
}

func NewServeMux() *asynq.ServeMux {
	return asynq.NewServeMux()
}
