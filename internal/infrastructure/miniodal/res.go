package miniodal

import (
	"context"

	"github.com/minio/minio-go/v7"
)

type Doer interface {
	context.Context
	Client() *minio.Client
}

type Res[T Doer] struct {
	val    T
	bucket string
	key    string
	err    error
}

type A interface {
	minio.UploadInfo | minio.ObjectInfo
}

func (r Res[T]) Err() error {
	return r.err
}

func (r Res[T]) Val() T {
	return r.val
}

func (r Res[T]) Unwrap() (T, string, string, error) {
	return r.val, r.bucket, r.key, r.err
}

func (r Res[T]) PreSignURL() (url string, err error) {
	client := externalCli() // 签名一定走外网
	u, err := client.PresignedGetObject(r.Val(), r.bucket, r.key, expireTime, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
