package platform

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type passwordDirectory interface {
	PasswordAccount(ctx context.Context, email string) (id, storedEmail, hash string, created time.Time, ok bool, err error)
	RegisterPassword(ctx context.Context, id, email, hash string) error
}

func (s *Service) LoginPassword(ctx context.Context, email, password string) (string, string, error) {
	directory, ok := s.source.(passwordDirectory)
	if !ok {
		return "", "", NewError(http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "当前数据源不支持本地密码登录")
	}
	id, storedEmail, hash, _, found, err := directory.PasswordAccount(ctx, email)
	if err != nil {
		return "", "", err
	}
	if !found || !strings.HasPrefix(hash, "$2") || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", "", unauthorized("邮箱或密码不正确")
	}
	return id, storedEmail, nil
}

func (s *Service) RegisterPassword(ctx context.Context, email, password string) (string, string, error) {
	directory, ok := s.source.(passwordDirectory)
	if !ok {
		return "", "", NewError(http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "当前数据源不支持本地密码注册")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(email) > 320 {
		return "", "", invalid("邮箱格式不正确")
	}
	if len(password) < 8 || len(password) > 72 {
		return "", "", invalid("密码长度需要在 8 到 72 个字符之间")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	id := uuid.NewString()
	if err := directory.RegisterPassword(ctx, id, email, string(hashed)); err != nil {
		return "", "", err
	}
	return id, email, nil
}
