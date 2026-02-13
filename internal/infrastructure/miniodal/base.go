package miniodal

import (
	"context"

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

func (d *Dal) Client() *minio.Client {
	if d.clientType == Internal {
		return internalCli()
	}
	return externalCli()
}
