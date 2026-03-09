package miniodal

import (
	"context"
	"iter"

	"github.com/minio/minio-go/v7"
)

type ClientType int

const (
	Internal ClientType = iota
	External
)

type Dal struct {
	context.Context
	clientType ClientType
}

func New(clientType ClientType) *Dal {
	return &Dal{
		clientType: clientType,
	}
}

func (d *Dal) Upload(ctx context.Context) *Uploader {
	d.Context = ctx
	return &Uploader{Dal: d}
}

func (d *Dal) Download(ctx context.Context) *Downloader {
	d.Context = ctx
	return &Downloader{Dal: d}
}

func (d *Dal) ListObjectsIter(ctx context.Context, bucketName string, opts minio.ListObjectsOptions) iter.Seq[minio.ObjectInfo] {
	client := externalCli()
	if d.clientType == Internal {
		client = internalCli()
	}
	if client == nil {
		return func(func(minio.ObjectInfo) bool) {}
	}
	return client.ListObjectsIter(ctx, bucketName, opts)
}
