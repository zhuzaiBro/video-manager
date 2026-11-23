package dejobsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestLatestCatchesUpPastPinnedAndDuplicateJobs(t *testing.T) {
	var calls []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Token") != "test-token" || r.URL.Query().Get("limit") != "20" {
			t.Error("bad upstream request")
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		calls = append(calls, n)
		var rows []map[string]any
		for i := 0; i < 20; i++ {
			id := 100 - (n-1)*19 - i
			j := map[string]any{"topicId": id, "createTime": id, "positionName": "岗位"}
			if n == 1 && i == 0 {
				j["isTopJob"] = true
				j["createTime"] = 1
			}
			rows = append(rows, j)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"page": map[string]int{"page": n, "total": 200}, "results": rows}})
	}))
	defer server.Close()
	f := newFeed("test-token")
	f.url = server.URL
	rows, latest, err := f.latest(context.Background(), 80)
	if err != nil || len(rows) != 21 || latest != 99 || len(calls) != 2 {
		t.Fatalf("catch-up: rows %d checkpoint %d calls %v err %v", len(rows), latest, calls, err)
	}
	calls = nil
	rows, latest, err = f.latest(context.Background(), 0)
	if err != nil || len(rows) != 20 || latest != 99 || len(calls) != 1 {
		t.Fatalf("bootstrap: rows %d checkpoint %d calls %v err %v", len(rows), latest, calls, err)
	}
}

func TestFeedRejectsErrorsWithoutLoggingBodyOrForwardingToken(t *testing.T) {
	for _, response := range []string{`{"success":false,"errorCode":401,"message":"private-upstream-message"}`, `{"success":true,"data":{"page":{"page":1},"results":null}}`, `{"success":true,"data":{"page":{"page":1},"results":[{}]}}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
		f := newFeed("test-token")
		f.url = s.URL
		_, cursor, err := f.latest(context.Background(), 77)
		s.Close()
		if err == nil || strings.Contains(err.Error(), "private-upstream") || cursor != 77 {
			t.Fatal("invalid response accepted or cursor advanced")
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect carrying credentials") }))
	defer target.Close()
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirect.Close()
	f := newFeed("test-token")
	f.url = redirect.URL
	if _, err := f.fetch(context.Background(), 1); err == nil {
		t.Fatal("accepted redirect")
	}
}
