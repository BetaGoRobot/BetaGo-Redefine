package miniodal

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xrequest"

	"github.com/kevinmatthe/zaplog"
	"github.com/minio/minio-go/v7"
)

type Uploader struct {
	*Dal
	info        minio.UploadInfo
	skipDup     bool // 是否跳过重复文件的上传
	innerData   []byte
	contentType string
}

type UploaderX[T any] struct {
	*Uploader

	r T
}

type UploaderReader UploaderX[io.ReadCloser]

func (d *Uploader) WithReader(r io.ReadCloser) *UploaderReader {
	// 先给读完
	innerData, _ := io.ReadAll(r)
	d.innerData = innerData
	newReader := io.NopCloser(bytes.NewReader(d.innerData))
	return &UploaderReader{Uploader: d, r: newReader}
}

func (d *Uploader) WithURL(url string) *UploaderReader {
	// 先给读完
	resp, err := xrequest.Req().SetContext(d).SetDoNotParseResponse(true).Get(url)
	if err != nil {
		logs.L().Ctx(d).Error("Get file failed", zaplog.Error(err))
	}
	innerData, _ := io.ReadAll(resp.RawResponse.Body)
	d.innerData = innerData
	newReader := io.NopCloser(bytes.NewReader(d.innerData))
	return &UploaderReader{Uploader: d, r: newReader}
}

func (d *Uploader) WithData(data []byte) *UploaderReader {
	d.innerData = data
	return &UploaderReader{Uploader: d, r: io.NopCloser(bytes.NewReader(d.innerData))}
}

func (d *Uploader) WithContentType(typ string) *Uploader {
	d.contentType = typ
	return d
}

func (d *Uploader) Data() []byte {
	return d.innerData
}

func (d *Uploader) ContentType() string {
	return d.contentType
}

func (d *Uploader) SkipDedup(dedup bool) *Uploader {
	d.skipDup = dedup
	return d
}

func FileExists(ctx context.Context, bucketName, objName string) (found bool, err error) {
	_, err = clientInternal.StatObject(ctx, bucketName, objName, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func TryGetFile(ctx context.Context, bucketName, objName string) (url string, err error) {
	if found, err := FileExists(ctx, bucketName, objName); err != nil {
		return "", err
	} else if found {
		u, err := clientExternal.PresignedGetObject(ctx, bucketName, objName, time.Minute*5, nil)
		if err != nil {
			return "", err
		}
		return u.String(), nil
	}
	return "", nil
}

func (d *UploaderReader) Do(bucketName, objName string, opts minio.PutObjectOptions) *Res[*UploaderReader] {
	defer d.r.Close()
	if d.skipDup {
		if found, err := FileExists(d, bucketName, objName); err != nil {
			return &Res[*UploaderReader]{val: d, bucket: bucketName, key: objName, err: err}
		} else if found {
			return &Res[*UploaderReader]{val: d, bucket: bucketName, key: objName, err: nil}
		}
	}
	info, err := clientInternal.PutObject(d, bucketName, objName, d.r, -1, opts)
	if err != nil {
		return &Res[*UploaderReader]{val: d, bucket: bucketName, key: objName, err: err}
	}
	d.info = info
	return &Res[*UploaderReader]{val: d, bucket: bucketName, key: objName}
}
