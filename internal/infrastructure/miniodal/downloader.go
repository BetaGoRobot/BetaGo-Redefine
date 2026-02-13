package miniodal

import "github.com/minio/minio-go/v7"

type Downloader struct {
	*Dal
	info minio.UploadInfo
}
