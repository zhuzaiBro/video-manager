package platform

import (
	"context"
	"time"
)

// AuthAccount is the subset of Supabase Auth needed for profile backfill and
// referral age checks. Callers never supply this object.
type AuthAccount struct {
	ID        string
	Email     string
	Phone     string
	CreatedAt time.Time
	Metadata  map[string]any
}

// Privileged is intentionally not a generic query API. Each method is one
// administrative operation that ordinary row level security cannot express.
// Service code may call it only after it has checked the caller.
type Privileged interface {
	GetAuthUser(ctx context.Context, userID string) (AuthAccount, bool, error)
	ListAuthUsers(ctx context.Context) ([]AuthAccount, error)
	UpsertProfiles(ctx context.Context, rows []map[string]any, ignoreDuplicates bool) error
	ReferralExists(ctx context.Context, referredUserID string) (bool, error)
	InsertReferral(ctx context.Context, row map[string]any) error
	ReadProfile(ctx context.Context, userID string) (map[string]any, bool, error)
	UpdateVip(ctx context.Context, userID string, level int, expiresAt any) error
	MarkConsultationPaid(ctx context.Context, requestID int, userID string, paidAt string) error
	ListProfiles(ctx context.Context) ([]map[string]any, error)
	ListWatchLogsSince(ctx context.Context, since time.Time) ([]map[string]any, error)
	ListActiveTelegramAdmins(ctx context.Context) ([]int64, error)
	UpsertTelegramAdmin(ctx context.Context, chatID, telegramUserID int64, username string) error
	TelegramAdminActive(ctx context.Context, chatID, telegramUserID int64) (bool, error)
	TelegramUpdateSeen(ctx context.Context, updateID int64) (bool, error)
	TelegramReplySession(ctx context.Context, chatID, messageID int64) (string, bool, error)
	InsertAdminMessage(ctx context.Context, sessionID, content string) (string, error)
	InsertTelegramLink(ctx context.Context, row map[string]any) error
}

// SelfLookup reads the authenticated Auth user with that user's own token.
type SelfLookup interface {
	FetchSelf(ctx context.Context, caller Caller) (AuthAccount, error)
}

// TelegramSender delivers an already authorized support message.
type TelegramSender interface {
	Send(ctx context.Context, chatID int64, text string) (int64, error)
}
