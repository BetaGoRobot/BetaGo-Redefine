package miniodal

import (
	"bytes"
	"io"

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

type UploaderData UploaderX[[]byte]

type UploaderReader UploaderX[io.ReadCloser]

func (d *Uploader) WithReader(r io.ReadCloser) *UploaderReader {
	// 先给读完
	innerData, _ := io.ReadAll(r)
	d.innerData = innerData
	newReader := io.NopCloser(bytes.NewReader(d.innerData))
	return &UploaderReader{Uploader: d, r: newReader}
}

func (d *Uploader) WithData(data []byte) *UploaderData {
	d.innerData = data
	return &UploaderData{Uploader: d, r: data}
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

func (d *Uploader) TryGetFile(bucketName, objName string) (found bool, err error) {
	_, err = clientInternal.StatObject(d, bucketName, objName, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *UploaderData) Do(bucketName, objName string, opts minio.PutObjectOptions) *Res[*UploaderData] {
	if d.skipDup {
		if found, err := d.TryGetFile(bucketName, objName); err != nil {
			return &Res[*UploaderData]{val: d, bucket: bucketName, key: objName, err: err}
		} else if found {
			return &Res[*UploaderData]{val: d, bucket: bucketName, key: objName, err: nil}
		}
	}
	r := io.NopCloser(bytes.NewReader(d.r))
	defer r.Close()
	info, err := clientInternal.PutObject(d, bucketName, objName, r, -1, opts)
	if err != nil {
		return &Res[*UploaderData]{val: d, bucket: bucketName, key: objName, err: err}
	}
	d.info = info
	return &Res[*UploaderData]{val: d, bucket: bucketName, key: objName}
}

func (d *UploaderReader) Do(bucketName, objName string, opts minio.PutObjectOptions) *Res[*UploaderReader] {
	defer d.r.Close()
	if d.skipDup {
		if found, err := d.TryGetFile(bucketName, objName); err != nil {
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
