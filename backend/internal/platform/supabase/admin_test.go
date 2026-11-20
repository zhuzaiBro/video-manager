package supabase

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zood/video-manager/internal/platform"
)

func TestAdminClientSendsServiceRoleAndRejectsAnonKey(t *testing.T) {
	var saw serviceCapture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw.authorization = r.Header.Get("Authorization")
		saw.apiKey = r.Header.Get("apikey")
		saw.path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		saw.body = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	anon := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoiYW5vbiJ9.sig"
	if _, err := NewAdmin(server.URL, anon); err == nil {
		t.Fatal("anon key was accepted as the admin client")
	}
	serviceRole := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoic2VydmljZV9yb2xlIn0.sig"
	admin, err := NewAdmin(server.URL, serviceRole)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.UpdateVip(context.Background(), "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", 2, nil); err != nil {
		t.Fatal(err)
	}
	if saw.authorization != "Bearer "+serviceRole || saw.apiKey != serviceRole {
		t.Fatalf("admin headers = %s %s", saw.authorization, saw.apiKey)
	}
	if saw.path != "/rest/v1/user_profiles" {
		t.Fatalf("path %s", saw.path)
	}
	if !containsAll(saw.body, `"vip_level":2`) || containsAll(saw.body, "is_admin") {
		t.Fatalf("vip update changed unexpected columns: %s", saw.body)
	}

	userClient, err := New(server.URL, anon)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = userClient.FetchSelf(context.Background(), platform.Caller{UserID: "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10", Token: "user-token"})
	if saw.authorization != "Bearer user-token" || saw.apiKey != anon {
		t.Fatalf("user self lookup used the wrong credential: %s %s", saw.authorization, saw.apiKey)
	}
}

type serviceCapture struct {
	authorization string
	apiKey        string
	path          string
	body          string
}

func containsAll(value, part string) bool {
	return len(part) == 0 || (len(value) >= len(part) && (value == part || indexOf(value, part) >= 0))
}

func indexOf(value, part string) int {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
