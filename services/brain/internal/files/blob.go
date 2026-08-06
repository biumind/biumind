// Blob — MinIO/S3 兼容客户端封装. minio-go SDK 是事实标准 S3 client,
// 同时支持 AWS S3 / MinIO / Tigris 等; bucket 接口统一。

package files

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Blob struct {
	client *minio.Client
	bucket string
}

type BlobConfig struct {
	Endpoint     string // e.g. "minio:9000" (host:port, 不带 scheme)
	AccessKey    string
	SecretKey    string
	UseSSL       bool
	Bucket       string // 默认 "biumind-files"
	Region       string // 可空
	EnsureBucket bool   // 启动时若不存在自动创建
}

func NewBlob(ctx context.Context, cfg BlobConfig) (*Blob, error) {
	if cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("files: BlobConfig.Endpoint/AccessKey/SecretKey required")
	}
	if cfg.Bucket == "" {
		cfg.Bucket = "biumind-files"
	}
	cli, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	if cfg.EnsureBucket {
		if err := ensureBucket(ctx, cli, cfg.Bucket, cfg.Region); err != nil {
			return nil, err
		}
	}
	return &Blob{client: cli, bucket: cfg.Bucket}, nil
}

func ensureBucket(ctx context.Context, cli *minio.Client, bucket, region string) error {
	exists, err := cli.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return cli.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region})
}

func (b *Blob) Bucket() string { return b.bucket }

// Put — 流式上传. size 已知传 size, 未知传 -1 触发 multipart。
// contentType 推荐传 (浏览器查看 / Content-Type 头透传)。
func (b *Blob) Put(ctx context.Context, objectKey string, r io.Reader, size int64, contentType string) error {
	_, err := b.client.PutObject(ctx, b.bucket, objectKey, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

// Get — 流式下载. 调用方读完后必须 Close。
func (b *Blob) Get(ctx context.Context, objectKey string) (io.ReadCloser, *minio.ObjectInfo, error) {
	obj, err := b.client.GetObject(ctx, b.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, nil, err
	}
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		// MinIO 通过 Stat 才能拿到 NoSuchKey 错误
		if isNotFound(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	return obj, &stat, nil
}

// Remove — 真实从 MinIO 删. 通常由清理 job 调用 (业务逻辑只 SoftDelete)。
func (b *Blob) Remove(ctx context.Context, objectKey string) error {
	return b.client.RemoveObject(ctx, b.bucket, objectKey, minio.RemoveObjectOptions{})
}

// Head — 取对象元信息 (size / etag / lastModified)。finalize 阶段用它
// 验证 client 是否真把字节 PUT 上来了, 同时拿真实 size 做 sha256 校验。
// 对象不存在返回 ErrNotFound。
func (b *Blob) Head(ctx context.Context, objectKey string) (minio.ObjectInfo, error) {
	info, err := b.client.StatObject(ctx, b.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return minio.ObjectInfo{}, ErrNotFound
		}
		return minio.ObjectInfo{}, err
	}
	return info, nil
}

// PresignPut — 生成短时效的预签名 PUT URL, 让 client 直传 MinIO,
// 不经过 brain 代理。contentType 写入签名后 client 必须 PUT 同样的
// Content-Type 头, 否则 MinIO 返回 SignatureDoesNotMatch (R1 已验证)。
//
// ttl 推荐 5–15 分钟: 太长泄露窗口大, 太短慢网用户踩。
func (b *Blob) PresignPut(ctx context.Context, objectKey string, ttl time.Duration, contentType string) (*url.URL, error) {
	headers := http.Header{}
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	return b.client.PresignHeader(ctx, http.MethodPut, b.bucket, objectKey, ttl, url.Values{}, headers)
}

// PresignGet — 生成短时效的预签名 GET URL, 给 LLM provider / 渲染场景
// 用。URL 在 ttl 窗口内任何持有者都能下载, 所以:
//   - 控制 ttl 在 15 分钟以内
//   - 不要写日志 (调用方需 redact)
//   - 用户隔离仍由调用方先验 (Store.Get 校验 user_id)
func (b *Blob) PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (*url.URL, error) {
	return b.client.PresignedGetObject(ctx, b.bucket, objectKey, ttl, url.Values{})
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" {
		return true
	}
	// 防御性: 错误信息含 "key does not exist"
	return strings.Contains(strings.ToLower(err.Error()), "does not exist")
}
