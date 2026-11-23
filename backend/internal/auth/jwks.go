package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksCache struct {
	mu        sync.Mutex
	fetchedAt time.Time
	keys      map[string]any
}

func (v *Validator) publicKey(kid string) (any, error) {
	if v.jwksURL == "" {
		return nil, fmt.Errorf("jwks url is empty")
	}
	key, ok := v.keys.lookup(kid)
	if !ok {
		if err := v.keys.refresh(v.jwksURL); err != nil {
			return nil, err
		}
		key, ok = v.keys.lookup(kid)
	}
	if !ok {
		return nil, fmt.Errorf("signing key %s not found", kid)
	}
	return key, nil
}

func (c *jwksCache) lookup(kid string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetchedAt) > 10*time.Minute {
		return nil, false
	}
	key, ok := c.keys[kid]
	return key, ok
}

func (c *jwksCache) refresh(url string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks status %d", response.StatusCode)
	}
	var set jwkSet
	if err := json.NewDecoder(response.Body).Decode(&set); err != nil {
		return err
	}
	keys := map[string]any{}
	for _, item := range set.Keys {
		public, err := item.publicKey()
		if err != nil || item.Kid == "" {
			continue
		}
		keys[item.Kid] = public
	}
	c.mu.Lock()
	c.keys = keys
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return nil
}

func (item jwk) publicKey() (any, error) {
	switch item.Kty {
	case "EC":
		if item.Crv != "P-256" {
			return nil, fmt.Errorf("unsupported curve %s", item.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(item.X)
		if err != nil {
			return nil, err
		}
		y, err := base64.RawURLEncoding.DecodeString(item.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(item.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(item.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(new(big.Int).SetBytes(eBytes).Int64())}, nil
	default:
		return nil, fmt.Errorf("unsupported key type %s", item.Kty)
	}
}
