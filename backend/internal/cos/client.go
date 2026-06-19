package cos

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tencentyun/cos-go-sdk-v5"
	"github.com/zood/video-manager/internal/config"
)

type Client struct {
	client       *cos.Client
	presignClient *cos.Client
	secretID     string
	secretKey    string
	bucket       string
	region       string
	customDomain string
}

func NewClient(cfg *config.Config) (*Client, error) {
	c := &Client{
		bucket:       cfg.COSBucket,
		region:       cfg.COSRegion,
		secretID:     cfg.COSSecretID,
		secretKey:    cfg.COSSecretKey,
		customDomain: strings.TrimRight(cfg.COSCustomDomain, "/"),
	}
	if cfg.COSBucket == "" {
		return c, nil
	}

	bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", cfg.COSBucket, cfg.COSRegion))
	if err != nil {
		return nil, err
	}

	transport := &cos.AuthorizationTransport{
		SecretID:  cfg.COSSecretID,
		SecretKey: cfg.COSSecretKey,
	}

	c.client = cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, &http.Client{Transport: transport})

	if c.customDomain != "" {
		domainURL, err := url.Parse(c.customDomain)
		if err != nil {
			return nil, fmt.Errorf("parse COS_CUSTOM_DOMAIN: %w", err)
		}
		c.presignClient = cos.NewClient(&cos.BaseURL{BucketURL: domainURL}, &http.Client{Transport: transport})
	} else {
		c.presignClient = c.client
	}

	return c, nil
}

func (c *Client) GetObject(ctx context.Context, key string) (*cos.Response, error) {
	if c.client == nil {
		return nil, fmt.Errorf("COS client not configured")
	}
	return c.client.Object.Get(ctx, key, nil)
}

func (c *Client) PutObject(ctx context.Context, key string, body []byte, contentType string) error {
	if c.client == nil {
		return fmt.Errorf("COS client not configured")
	}
	_, err := c.client.Object.Put(ctx, key, bytes.NewReader(body), &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType: contentType,
		},
	})
	return err
}

func (c *Client) PresignedGetURL(ctx context.Context, key string, expire time.Duration) (string, error) {
	if c.presignClient == nil {
		return "", fmt.Errorf("COS client not configured")
	}
	u, err := c.presignClient.Object.GetPresignedURL(ctx, http.MethodGet, key, c.secretID, c.secretKey, expire, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (c *Client) UploadDir(ctx context.Context, localDir, cosPrefix string) error {
	if c.client == nil {
		return nil
	}

	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}

		key := filepath.ToSlash(filepath.Join(cosPrefix, rel))
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		contentType := contentTypeForFile(path)
		_, err = c.client.Object.Put(ctx, key, f, &cos.ObjectPutOptions{
			ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
				ContentType: contentType,
			},
		})
		return err
	})
}

func (c *Client) ObjectKey(videoID uuid.UUID, filename string) string {
	return fmt.Sprintf("videos/%s/%s", videoID, filename)
}

func (c *Client) Prefix(videoID uuid.UUID) string {
	return fmt.Sprintf("videos/%s", videoID)
}

func (c *Client) PlayListKey(videoID uuid.UUID) string {
	return fmt.Sprintf("videos/%s/_play.m3u8", videoID)
}

func contentTypeForFile(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".mp4", ".m4s":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}
