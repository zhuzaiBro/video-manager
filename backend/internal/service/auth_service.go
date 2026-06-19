package service

import (
	"errors"

	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
)

var (
	ErrAuthInvalidToken  = errors.New("invalid supabase token")
	ErrAuthMissingSecret = errors.New("supabase jwt secret not configured")
)

type AuthService struct {
	cfg       *config.Config
	validator *auth.Validator
}

func NewAuthService(cfg *config.Config, validator *auth.Validator) *AuthService {
	return &AuthService{cfg: cfg, validator: validator}
}

type TencentTokenResult struct {
	UserID       string `json:"userId"`
	TencentToken string `json:"tencentToken"`
	ProxyBaseURL string `json:"proxyBaseUrl"`
	ExpireAt     int64  `json:"expireAt"`
}

func (s *AuthService) ExchangeTencentToken(supabaseToken string) (*TencentTokenResult, error) {
	claims, err := s.validator.ParseSupabaseToken(supabaseToken)
	if err != nil {
		if errors.Is(err, auth.ErrMissingSecret) {
			return nil, ErrAuthMissingSecret
		}
		return nil, ErrAuthInvalidToken
	}

	tencentToken, expireAt, err := s.validator.IssueTencentToken(claims.Subject, claims.Email)
	if err != nil {
		return nil, err
	}

	return &TencentTokenResult{
		UserID:       claims.Subject,
		TencentToken: tencentToken,
		ProxyBaseURL: s.cfg.APIBaseURL,
		ExpireAt:     expireAt,
	}, nil
}
