package platform

import "context"

type Result struct {
	Rows  []map[string]any
	Count *int
}

// Source is an internal data port. HTTP handlers never pass raw SQL or a
// client-selected table; every call is built by Service from a fixed query.
type Source interface {
	Select(ctx context.Context, caller Caller, query Query) (Result, error)
	Write(ctx context.Context, caller Caller, method string, query Query, payload any) (Result, error)
}

// ObjectStorage uploads and signs files with the caller's own credential.
type ObjectStorage interface {
	Upload(ctx context.Context, caller Caller, bucket, path, contentType string, body []byte) error
	SignedURL(ctx context.Context, caller Caller, bucket, path string, expiresIn int) (string, error)
	PublicURL(bucket, path string) string
}
