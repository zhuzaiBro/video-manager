package dejobsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const pageSize = 20
const feedURL = "https://dejob.ai/api/worker/topics"

type rateLimited struct{ retryAt time.Time }

func (e *rateLimited) Error() string {
	return "DeJob HTTP 429; retry after " + e.retryAt.UTC().Format(time.RFC3339)
}

func retryTime(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if until, err := http.ParseTime(value); err == nil && until.After(now) {
		return until
	}
	return now.Add(time.Hour)
}

type page struct {
	Results []json.RawMessage `json:"results"`
	Page    struct {
		Page  int `json:"page"`
		Total int `json:"total"`
	} `json:"page"`
}

type feed struct {
	url    string
	token  string
	client *http.Client
}

func newFeed(token string) feed {
	return feed{feedURL, token, &http.Client{
		Timeout: 25 * time.Second,
		// The user token is scoped to this precise HTTPS endpoint.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (f feed) fetch(ctx context.Context, number int) (*page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s?page=%d&limit=%d", f.url, number, pageSize), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-User-Token", f.token)
	req.Header.Set("Referer", "https://dejob.ai/job")
	req.Header.Set("Accept-Language", "zh")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ZoodJobSync/1.0 (+https://www.zood.work/jobs)")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DeJob request on page %d: %w", number, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &rateLimited{retryTime(resp.Header.Get("Retry-After"), time.Now())}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DeJob page %d HTTP %d", number, resp.StatusCode)
	}
	var envelope struct {
		Success bool  `json:"success"`
		Code    int   `json:"errorCode"`
		Data    *page `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("invalid DeJob JSON on page %d", number)
	}
	if !envelope.Success || envelope.Code != 0 || envelope.Data == nil || envelope.Data.Results == nil || envelope.Data.Page.Page != number {
		return nil, fmt.Errorf("invalid DeJob response on page %d (code %d)", number, envelope.Code)
	}
	return envelope.Data, nil
}

// The first import takes the current front page. Later imports follow all new
// pages, even after downtime. Pinned jobs can appear ahead of newer listings.
func (f feed) latest(ctx context.Context, checkpoint int64) ([]json.RawMessage, int64, error) {
	var jobs []json.RawMessage
	newest := checkpoint
	seen := map[int64]bool{}
	for number := 1; ; number++ {
		p, err := f.fetch(ctx, number)
		if err != nil {
			return nil, checkpoint, err
		}
		older, added := false, false
		for _, raw := range p.Results {
			var j struct {
				ID      int64  `json:"topicId"`
				Title   string `json:"positionName"`
				Created int64  `json:"createTime"`
				Pinned  bool   `json:"isTopJob"`
			}
			if json.Unmarshal(raw, &j) != nil || j.ID <= 0 || j.Created <= 0 || strings.TrimSpace(j.Title) == "" {
				return nil, checkpoint, fmt.Errorf("invalid job identity or publication time on page %d", number)
			}
			if !j.Pinned && j.Created < checkpoint {
				older = true
			}
			if seen[j.ID] {
				continue
			}
			seen[j.ID], added = true, true
			// Refresh page one (including edits); subsequent pages only fill the gap.
			if number == 1 || j.Created >= checkpoint {
				jobs = append(jobs, raw)
			}
			if !j.Pinned && j.Created > newest {
				newest = j.Created
			}
		}
		if len(p.Results) < pageSize || number*pageSize >= p.Page.Total || older || (checkpoint == 0 && newest > 0) {
			break
		}
		if !added {
			return nil, checkpoint, errors.New("DeJob pagination did not advance")
		}
	}
	return jobs, newest, nil
}
