package support

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/config"
	cache "github.com/zood/video-manager/internal/redis"
)

func TestWebTypingLeasesAndRealtime(t *testing.T) {
	addr := os.Getenv("SUPPORT_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("no disposable Redis instance")
	}
	ctx := context.Background()
	key := uuid.NewString()
	events := make(chan struct{}, 8)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event struct {
			Messages []struct {
				Topic, Event string
				Payload      map[string]any
			}
		}
		if r.URL.Path != "/realtime/v1/api/broadcast" || json.NewDecoder(r.Body).Decode(&event) != nil || len(event.Messages) != 1 ||
			event.Messages[0].Event != "typing" || event.Messages[0].Topic != (&Service{key: []byte("fixture")}).Topic(key) || len(event.Messages[0].Payload) != 0 {
			t.Error("expected an empty typing wake-up for the customer conversation")
		}
		events <- struct{}{}
	}))
	defer api.Close()
	c := cache.NewClient(&config.Config{RedisAddr: addr})
	s := &Service{typing: c, key: []byte("fixture"), realtime: api.URL, publicKey: "fixture", client: api.Client()}
	otherServer := &Service{typing: cache.NewClient(&config.Config{RedisAddr: addr})}
	update := func(editor string, active bool) {
		t.Helper()
		if err := s.SetTyping(ctx, key, editor, active); err != nil {
			t.Fatal(err)
		}
		select {
		case <-events:
		case <-time.After(time.Second):
			t.Fatal("broadcast missing")
		}
	}
	check := func(session string, active bool) {
		t.Helper()
		ms, err := otherServer.TypingFor(ctx, session)
		if err != nil || (ms > 0) != active || ms > 7000 {
			t.Fatalf("typing: %d ms, %v", ms, err)
		}
	}
	check(key, false)
	update("first", true)
	check(key, true)
	check(uuid.NewString(), false)
	update("second", true)
	update("first", false)
	check(key, true)
	update("second", false)
	check(key, false)
	update("abandoned", true)
	time.Sleep(7100 * time.Millisecond)
	check(key, false)
}
