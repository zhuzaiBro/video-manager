package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zood/video-manager/internal/config"
)

func TestJWKSAcceptsProjectTokenAndRejectsWrongIssuer(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "test", "kty": "EC", "crv": "P-256", "alg": "ES256",
			"x": base64.RawURLEncoding.EncodeToString(private.X.FillBytes(make([]byte, 32))),
			"y": base64.RawURLEncoding.EncodeToString(private.Y.FillBytes(make([]byte, 32))),
		}}})
	}))
	defer server.Close()
	validator := NewValidator(&config.Config{
		SupabaseURL:     "https://gmfuorleragagsqbkajh.supabase.co",
		SupabaseJWKSURL: server.URL,
	})
	userID := "8b1b5d4e-6a7c-4d11-9a1e-0d4c2b6f8a10"
	signed := signES256(t, private, SupabaseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID, Issuer: "https://gmfuorleragagsqbkajh.supabase.co/auth/v1",
			Audience: []string{"authenticated"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Role: "authenticated", Email: "ada@example.com",
	})
	claims, err := validator.ParseSupabaseToken(signed)
	if err != nil || claims.Subject != userID {
		t.Fatalf("valid token: %+v %v", claims, err)
	}
	retired := signES256(t, private, SupabaseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID, Issuer: "https://amnksdnwvwmmmvqoxzyj.supabase.co/auth/v1",
			Audience: []string{"authenticated"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Role: "authenticated",
	})
	if _, err := validator.ParseSupabaseToken(retired); err == nil {
		t.Fatal("retired project token was accepted")
	}
	adminish := signES256(t, private, SupabaseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID, Issuer: "https://gmfuorleragagsqbkajh.supabase.co/auth/v1",
			Audience: []string{"authenticated"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Role: "service_role",
	})
	if _, err := validator.ParseSupabaseToken(adminish); err == nil {
		t.Fatal("service role token was accepted")
	}
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, claims SupabaseClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = "test"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
