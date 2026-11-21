package repository

import (
	"strings"
	"testing"
)

func TestVideoDSNDoesNotSendDriverParameter(t *testing.T) {
	dsn := ensurePgBouncerCompat("postgres://zood_app@127.0.0.1:55432/video_manager?sslmode=disable")
	if strings.Contains(dsn, "prefer_simple_protocol") {
		t.Fatalf("DSN must not contain prefer_simple_protocol: %s", dsn)
	}
}
