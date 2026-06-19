package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zood/video-manager/internal/config"
)

var (
	ErrInvalidToken  = errors.New("invalid token")
	ErrMissingSecret = errors.New("supabase jwt secret not configured")
	ErrTokenExpired  = errors.New("token expired")
	ErrUnresolvedVar = errors.New("unresolved token variable")
)

const tokenTypeTencent = "tencent"

type SupabaseClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
	Role  string `json:"role"`
}

type TencentClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email,omitempty"`
	Type  string `json:"type"`
}

type ResolveResult struct {
	UserID string
	Source string // tencent | supabase
}

type Validator struct {
	supabaseSecret string
	jwtSecret      string
	tokenExpire    time.Duration
}

func NewValidator(cfg *config.Config) *Validator {
	return &Validator{
		supabaseSecret: cfg.SupabaseJWTSecret,
		jwtSecret:      cfg.JWTSecret,
		tokenExpire:    time.Duration(cfg.TokenExpireSec) * time.Second,
	}
}

func (v *Validator) ResolveUser(tokenString string) (*ResolveResult, error) {
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return nil, ErrInvalidToken
	}
	if strings.Contains(tokenString, "{{") || strings.Contains(tokenString, "}}") {
		return nil, ErrUnresolvedVar
	}
	if strings.Count(tokenString, ".") != 2 {
		return nil, fmt.Errorf("%w: token must be a JWT with 3 segments", ErrInvalidToken)
	}

	if claims, err := v.ParseTencentToken(tokenString); err == nil {
		return &ResolveResult{UserID: claims.Subject, Source: "tencent"}, nil
	}
	if claims, err := v.ParseSupabaseToken(tokenString); err == nil {
		return &ResolveResult{UserID: claims.Subject, Source: "supabase"}, nil
	}

	return nil, ErrInvalidToken
}

func (v *Validator) ParseSupabaseToken(tokenString string) (*SupabaseClaims, error) {
	if v.supabaseSecret == "" {
		return nil, ErrMissingSecret
	}

	token, err := jwt.ParseWithClaims(tokenString, &SupabaseClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(v.supabaseSecret), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*SupabaseClaims)
	if !ok || !token.Valid || claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (v *Validator) IssueTencentToken(userID, email string) (string, int64, error) {
	expireAt := time.Now().Add(v.tokenExpire)
	claims := TencentClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(expireAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "video-manager",
		},
		Email: email,
		Type:  tokenTypeTencent,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(v.jwtSecret))
	if err != nil {
		return "", 0, err
	}
	return signed, expireAt.Unix(), nil
}

func (v *Validator) ParseTencentToken(tokenString string) (*TencentClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TencentClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(v.jwtSecret), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*TencentClaims)
	if !ok || !token.Valid || claims.Subject == "" || claims.Type != tokenTypeTencent {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
