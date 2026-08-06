// Package blob 是 services/aigc 的 MinIO/S3 兼容客户端封装.
//
// 与 brain/internal/files/blob.go 的区别: aigc 用多桶 (outputs / derivatives
// / uploads / public / temp), 所以 client 不绑定单 bucket, Get 时按逻辑桶名
// 取真实桶。minio-go SDK 是仓库既有的事实标准 S3 client (brain 已用)。
package blob

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound — 对象不存在 (NoSuchKey / NoSuchBucket).
var ErrNotFound = errors.New("aigc/blob: object not found")

// ObjectInfo — Get 返回的最小元信息. 不暴露 minio.ObjectInfo, 让 api 层
// 能定义 interface + 注入 fake 单测 (不依赖 minio 类型)。
type ObjectInfo struct {
	Size        int64
	ContentType string
}

// Client 持有 minio.Client + 逻辑桶名 → 真实桶名 的映射.
type Client struct {
	mc      *minio.Client
	buckets map[string]string // "outputs" → "biumind-aigc-outputs"
}

type Config struct {
	Endpoint  string // host:port, 不带 scheme
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string

	BucketOutputs     string
	BucketDerivatives string
	BucketUploads     string
	BucketPublic      string
	BucketTemp        string
}

// New 建 client. Endpoint/AccessKey/SecretKey 任一为空返 nil, nil — 让
// caller 据此决定不挂载下载路由 (dev 无 MinIO 时优雅降级)。
//
// Endpoint 兼容带 scheme (AIGC_S3_ENDPOINT=http://minio:9000) — minio-go 要
// host:port, 这里 strip 掉 scheme; https:// 前缀同时推断 Secure=true。
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, nil
	}
	endpoint := cfg.Endpoint
	secure := cfg.UseSSL
	if strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimPrefix(endpoint, "https://")
		secure = true
	} else if strings.HasPrefix(endpoint, "http://") {
		endpoint = strings.TrimPrefix(endpoint, "http://")
		secure = false
	}
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		mc: mc,
		buckets: map[string]string{
			"outputs":     cfg.BucketOutputs,
			"derivatives": cfg.BucketDerivatives,
			"uploads":     cfg.BucketUploads,
			"public":      cfg.BucketPublic,
			"temp":        cfg.BucketTemp,
		},
	}, nil
}

// Get 从逻辑桶 (logicalBucket: "outputs"/"derivatives"/...) 流式取对象.
// 返回的 ReadCloser 调用方读完必须 Close。对象不存在返 ErrNotFound。
func (c *Client) Get(ctx context.Context, logicalBucket, objectKey string) (io.ReadCloser, *ObjectInfo, error) {
	bucket := c.buckets[logicalBucket]
	if bucket == "" {
		return nil, nil, errors.New("aigc/blob: unknown bucket " + logicalBucket)
	}
	obj, err := c.mc.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, nil, err
	}
	// minio-go 的 NoSuchKey 要在 Stat 才暴露.
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if isNotFound(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	return obj, &ObjectInfo{Size: stat.Size, ContentType: stat.ContentType}, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "does not exist")
}
