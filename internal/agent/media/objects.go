package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Object = domain.Object
type ObjectStore interface {
	Put(context.Context, domain.SourceRef, domain.Media, io.Reader, int64) (Object, error)
}
type Downloader interface {
	Download(context.Context, domain.Message, domain.Media) (io.ReadCloser, int64, error)
}
type MinIOConfig struct {
	Endpoint, AccessKey, SecretKey, Bucket string
	Secure                                 bool
	MaxBytes                               int64
	Timeout                                time.Duration
}
type MinIO struct {
	client *minio.Client
	config MinIOConfig
}

func NewMinIO(c MinIOConfig) (*MinIO, error) {
	if c.Endpoint == "" || c.AccessKey == "" || c.SecretKey == "" || c.Bucket == "" {
		return nil, fmt.Errorf("invalid media object configuration")
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 32 << 20
	}
	if c.Timeout == 0 {
		c.Timeout = time.Minute
	}
	if c.MaxBytes < 1 || c.MaxBytes > 256<<20 || c.Timeout <= 0 || c.Timeout > 5*time.Minute {
		return nil, ErrLimit
	}
	client, err := minio.New(c.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""), Secure: c.Secure})
	if err != nil {
		return nil, fmt.Errorf("invalid media object endpoint")
	}
	return &MinIO{client, c}, nil
}
func objectKey(s domain.SourceRef, m domain.Media) string {
	b, _ := json.Marshal([]any{s.Scope.Key(), s.MessageID, s.Version, m.ResourceID, m.Sequence})
	h := sha256.Sum256(b)
	return s.Scope.Key() + "/" + hex.EncodeToString(h[:])
}
func (m *MinIO) Put(ctx context.Context, s domain.SourceRef, media domain.Media, r io.Reader, size int64) (Object, error) {
	if err := s.Validate(); err != nil {
		return Object{}, err
	}
	if size < 0 || size > m.config.MaxBytes {
		return Object{}, ErrLimit
	}
	ctx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	defer cancel()
	data, err := boundedRead(r, size)
	if err != nil {
		return Object{}, err
	}
	if int64(len(data)) != size {
		return Object{}, fmt.Errorf("media object size mismatch")
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	key := objectKey(s, media)
	meta := map[string]string{"scope": s.Scope.Key(), "message": s.MessageID, "source-version": strconv.FormatInt(s.Version, 10), "author": s.AuthorID, "resource": media.ResourceID, "sha256": digest}
	opts := minio.PutObjectOptions{ContentType: media.MIME, UserMetadata: meta, DisableMultipart: true}
	opts.SetMatchETagExcept("*")
	info, err := m.client.PutObject(ctx, m.config.Bucket, key, bytes.NewReader(data), size, opts)
	if err != nil {
		if minio.ToErrorResponse(err).Code != "PreconditionFailed" {
			return Object{}, fmt.Errorf("media object upload failed")
		}
		existing, e := m.client.StatObject(ctx, m.config.Bucket, key, minio.StatObjectOptions{})
		if e != nil {
			return Object{}, fmt.Errorf("media original verification failed")
		}
		if existing.Size != size || existing.UserMetadata["Sha256"] != digest {
			return Object{}, domain.ErrConflict
		}
		info.Size = existing.Size
		info.ETag = existing.ETag
	}
	return Object{Key: key, MIME: media.MIME, ETag: info.ETag, Size: info.Size, Source: s, ResourceID: media.ResourceID, Sequence: media.Sequence}, nil
}

type originalReader struct {
	io.Reader
	closer io.Closer
	cancel context.CancelFunc
}

func (r *originalReader) Close() error { r.cancel(); return r.closer.Close() }
func (m *MinIO) Get(ctx context.Context, o Object) (io.ReadCloser, error) {
	if err := o.Source.Validate(); err != nil {
		return nil, err
	}
	if o.Size < 0 || o.Size > m.config.MaxBytes {
		return nil, ErrLimit
	}
	if o.Key != objectKey(o.Source, domain.Media{ResourceID: o.ResourceID, Sequence: o.Sequence}) {
		return nil, domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	obj, err := m.client.GetObject(ctx, m.config.Bucket, o.Key, minio.GetObjectOptions{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("media object read failed")
	}
	info, err := obj.Stat()
	if err != nil || info.Size != o.Size || info.Size > m.config.MaxBytes {
		obj.Close()
		cancel()
		return nil, fmt.Errorf("media object verification failed")
	}
	return &originalReader{Reader: io.LimitReader(obj, o.Size), closer: obj, cancel: cancel}, nil
}

// Initialize creates only the configured dedicated originals bucket.
func (m *MinIO) Initialize(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	defer cancel()
	exists, err := m.client.BucketExists(ctx, m.config.Bucket)
	if err != nil {
		return fmt.Errorf("media object bucket unavailable")
	}
	if exists {
		return nil
	}
	if err = m.client.MakeBucket(ctx, m.config.Bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("media object bucket initialization failed")
	}
	return nil
}
