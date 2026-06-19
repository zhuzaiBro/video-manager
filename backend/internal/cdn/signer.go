package cdn

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zood/video-manager/internal/config"
)

type Signer struct {
	domain    string
	signKey   string
	expireSec int
}

func NewSigner(cfg *config.Config) *Signer {
	return &Signer{
		domain:    strings.TrimRight(cfg.CDNDomain, "/"),
		signKey:   cfg.CDNSignKey,
		expireSec: cfg.CDNExpireSec,
	}
}

type SignedURL struct {
	URL      string
	ExpireAt int64
}

// Sign generates a Type-A CDN signed URL: ?sign=md5(key+path+timestamp)&t=timestamp
func (s *Signer) Sign(objectPath string) (*SignedURL, error) {
	path := objectPath
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	expireAt := time.Now().Add(time.Duration(s.expireSec) * time.Second).Unix()

	if s.domain == "" {
		// 开发模式：返回本地路径占位
		return &SignedURL{
			URL:      "file://" + path,
			ExpireAt: expireAt,
		}, nil
	}
	timestamp := strconv.FormatInt(expireAt, 10)

	signStr := s.signKey + path + timestamp
	hash := md5.Sum([]byte(signStr))
	sign := hex.EncodeToString(hash[:])

	url := fmt.Sprintf("%s%s?sign=%s&t=%s", s.domain, path, sign, timestamp)
	return &SignedURL{
		URL:      url,
		ExpireAt: expireAt,
	}, nil
}
