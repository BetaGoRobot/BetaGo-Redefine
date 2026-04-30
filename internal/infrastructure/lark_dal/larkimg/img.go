package larkimg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xrequest"
	"github.com/bytedance/sonic"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/minio/minio-go/v7"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

// DownImgFromMsgSync 从Msg中下载附件
//
//	@param ctx context.Context
//	@param msgID string
//	@param fileKey string
//	@param fileType string
//	@return image []byte
//	@return err error
//	@author kevinmatthe
//	@update 2025-04-27 20:15:38
func DownImgFromMsgSync(ctx context.Context, msgID, fileType, fileKey string) (b64Data string, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.Key("msgID").String(msgID),
		attribute.Key("fileKey").String(fileKey),
		attribute.Key("fileType").String(fileType),
	)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(msgID).
		FileKey(fileKey).
		Type("image").
		Build()
	// 发起请求
	resp, err := lark_dal.Client().Im.V1.MessageResource.Get(ctx, req)
	// 处理错误
	if err != nil {
		return
	}

	// 服务端错误处理
	if !resp.Success() {
		return "", errors.New(resp.Error())
	}

	reader, contentType, suffix, err := readAndDetectFormat(resp.File)
	if err != nil {
		return
	}

	dal := miniodal.New(miniodal.Internal)
	res := dal.Upload(ctx).WithContentType(contentType).WithReader(reader).Do("larkchat", filepath.Join("chat_image", fileType, fileKey+suffix), minio.PutObjectOptions{})
	if res.Err() != nil {
		return "", res.Err()
	}

	b64Data = res.B64Data()
	logs.L().Ctx(ctx).Info("upload pic to minio success", zap.String("file_key", fileKey),
		zap.String("file_type", fileType))
	return
}

// DownImgFromMsgAsync 从Msg中下载附件
//
//	@param ctx context.Context
//	@param msgID string
//	@param fileKey string
//	@param fileType string
//	@return image []byte
//	@return err error
//	@author kevinmatthe
//	@update 2025-04-27 20:15:38
func DownImgFromMsgAsync(ctx context.Context, msgID, fileType, fileKey string) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.Key("msgID").String(msgID),
		attribute.Key("fileKey").String(fileKey),
		attribute.Key("fileType").String(fileType),
	)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(msgID).
		FileKey(fileKey).
		Type(fileType).
		Build()
	// 发起请求
	resp, err := lark_dal.Client().Im.V1.MessageResource.Get(ctx, req)
	// 处理错误
	if err != nil {
		logs.L().Ctx(ctx).Error("get message resource error", zap.String("file_key", fileKey), zap.String("file_type", fileType), zap.Error(err))
		return
	}

	// 服务端错误处理
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("get message resource error", zap.String("file_key", fileKey), zap.String("file_type", fileType), zap.Error(resp))
		return
	}

	go func() {
		reader, contentType, suffix, err := readAndDetectFormat(resp.File)
		if err != nil {
			return
		}

		dal := miniodal.New(miniodal.Internal)
		res := dal.Upload(ctx).WithContentType(contentType).WithReader(reader).Do("larkchat", filepath.Join("chat_image", fileType, fileKey+suffix), minio.PutObjectOptions{})
		if res.Err() != nil {
			logs.L().Ctx(ctx).Warn("upload pic to minio error", zap.String("file_key", fileKey), zap.String("file_type", fileType))
			return
		}
		u, preSignErr := res.PreSignURL()
		if preSignErr != nil {
			logs.L().Ctx(ctx).Warn("get presign url error", zap.Error(preSignErr))
			return
		}
		logs.L().Ctx(ctx).Info("upload pic to minio success", zap.String("file_type", fileType),
			zap.String("url", u))
	}()

	return
}

// 检测图片格式
func detectImageFormat(header []byte) (string, string, error) {
	// 检查文件头并返回格式
	switch {
	case bytes.HasPrefix(header, []byte{0x89, 0x50, 0x4E, 0x47}): // PNG
		return "image/png", ".png", nil
	case bytes.HasPrefix(header, []byte{0x47, 0x49, 0x46, 0x38}): // GIF
		return "image/gif", ".gif", nil
	case bytes.HasPrefix(header, []byte{0xFF, 0xD8, 0xFF}): // JPEG
		return "image/jpeg", ".jpg", nil
	default:
		return "", "", fmt.Errorf("unknown image format")
	}
}

// 从 io.Reader 中读取完整的字节数据并检测文件头
func readAndDetectFormat(reader io.Reader) (io.ReadCloser, string, string, error) {
	// 读取文件头（例如，读取 8 个字节）
	header := make([]byte, 8)
	_, err := reader.Read(header)
	if err != nil {
		return nil, "", "", fmt.Errorf("error reading file header: %v", err)
	}

	// 根据文件头检测格式
	contentType, suffix, err := detectImageFormat(header)
	if err != nil {
		return nil, "", "", err
	}

	return wrapReaderWithHeader(header, reader), contentType, suffix, nil
}

// 封装一个新的 io.ReadCloser，从头部+原始Reader组成
func wrapReaderWithHeader(header []byte, r io.Reader) io.ReadCloser {
	return &readCloser{
		Reader: io.MultiReader(bytes.NewReader(header), r),
	}
}

// 自定义 ReadCloser
type readCloser struct {
	io.Reader
}

func (rc *readCloser) Close() error {
	// 如果原始 r 是 ReadCloser，可以在这里关闭底层流
	// 这里为了简单，假设不用关闭底层流或者由外部管理
	return nil
}

type postData struct {
	Title   string           `json:"title"`
	Content [][]*contentData `json:"content"`
}

type contentData struct {
	Tag      string `json:"tag"`
	ImageKey string `json:"image_key"`
}

// GetAllImgTagFromMsg 从消息事件中获取所有图片
//
//	@param event *larkim.P2MessageReceiveV1
//	@author kevinmatthe
//	@update 2025-04-28 19:47:21
func GetAllImgTagFromMsg(ctx context.Context, message *larkim.Message) (imageKeys iter.Seq[string], err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("message", larkcore.Prettify(message), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if msgType := *message.MsgType; msgType == larkim.MsgTypeImage {
		var msg *larkim.MessageImage
		msg, err = jsonTrans[larkim.MessageImage](*message.Body.Content)
		if err != nil {
			return
		}
		return func(yield func(string) bool) {
			if !yield(msg.ImageKey) {
				return
			}
		}, nil
	} else if msgType == larkim.MsgTypePost {
		var msg *postData
		msg, err = jsonTrans[postData](*message.Body.Content)
		if err != nil {
			return
		}
		return func(yield func(string) bool) {
			for key := range getAllImage(ctx, msg) {
				if !yield(key) {
					return
				}
			}
		}, nil
	}
	return nil, nil
}

// GetAllImageFromMsgEvent 从消息事件中获取所有图片
//
//	@param event *larkim.P2MessageReceiveV1
//	@author kevinmatthe
//	@update 2025-04-28 19:47:21
func GetAllImageFromMsgEvent(ctx context.Context, message *larkim.EventMessage) (imageKeys iter.Seq[string], err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("message", larkcore.Prettify(message), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if msgType := *message.MessageType; msgType == larkim.MsgTypeImage {
		var msg *larkim.MessageImage
		msg, err = jsonTrans[larkim.MessageImage](*message.Content)
		if err != nil {
			return
		}
		return func(yield func(string) bool) {
			if !yield(msg.ImageKey) {
				return
			}
		}, nil
	} else if msgType == larkim.MsgTypePost {
		var msg *postData
		msg, err = jsonTrans[postData](*message.Content)
		if err != nil {
			return
		}
		return func(yield func(string) bool) {
			for key := range getAllImage(ctx, msg) {
				if !yield(key) {
					return
				}
			}
		}, nil
	}
	return
}

func getAllImage(ctx context.Context, msg *postData) iter.Seq[string] {
	return func(yield func(string) bool) {
		_, span := otel.Start(ctx)
		defer span.End()
		for _, elements := range msg.Content {
			for _, element := range elements {
				if element.Tag == "img" {
					if !yield(element.ImageKey) {
						return
					}
				}
			}
		}
	}
}

func jsonTrans[T any](s string) (*T, error) {
	t := new(T)
	err := sonic.UnmarshalString(s, t)
	if err != nil {
		return t, err
	}
	return t, nil
}

func GetAllImgURLFromMsg(ctx context.Context, msgID string) (resSeq iter.Seq[string], err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	resp := larkmsg.GetMsgFullByID(ctx, msgID)
	if len(resp.Data.Items) == 0 {
		return utils.NilIter[string](), nil
	}
	msg := resp.Data.Items[0]
	if msg == nil {
		return utils.NilIter[string](), errors.New("no message found")
	}
	if msg.Sender.Id == nil {
		return utils.NilIter[string](), errors.New("message is not sent by bot")
	}
	seq, err := GetAllImgTagFromMsg(ctx, msg)
	if err != nil {
		return utils.NilIter[string](), err
	}
	if seq == nil {
		return utils.NilIter[string](), err
	}
	return func(yield func(string) bool) {
		ctx, span := otel.Start(ctx)
		defer span.End()
		defer func() { otel.RecordError(span, err) }()

		for imageKey := range seq {
			url, err := DownImgFromMsgSync(ctx, *msg.MessageId, *msg.MsgType, imageKey)
			if err != nil {
				return
			}
			if !yield(url) {
				return
			}
		}
	}, nil
}

func GetAllImgURLFromParent(ctx context.Context, data *larkim.P2MessageReceiveV1) (iter.Seq[string], error) {
	if data.Event.Message.ThreadId != nil {
		// 话题模式 找图片
		resp, err := lark_dal.Client().Im.Message.List(ctx,
			larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return utils.NilIter[string](), err
		}
		if !resp.Success() {
			return utils.NilIter[string](), errors.New(resp.Error())
		}
		return func(yield func(string) bool) {
			for _, msg := range resp.Data.Items {
				if msg.MsgType == nil || (*msg.MsgType != larkim.MsgTypeImage && *msg.MsgType != larkim.MsgTypePost) {
					continue
				}
				seq, err := GetAllImgURLFromMsg(ctx, *msg.MessageId)
				if err != nil {
					return
				}
				if seq != nil {
					for url := range seq {
						if !yield(url) {
							return
						}
					}
				}
			}
		}, nil
	} else if data.Event.Message.ParentId != nil {
		// 检查是否已经处理过父消息
		return func(yield func(string) bool) {
			seq, err := GetAllImgURLFromMsg(ctx, *data.Event.Message.ParentId)
			if err != nil {
				return
			}
			if seq != nil {
				for url := range seq {
					if !yield(url) {
						return
					}
				}
			}
		}, nil
	}
	return utils.NilIter[string](), nil
}

func GetAndResizePicFromURL(ctx context.Context, imageURL string) (res []byte, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("img_url", imageURL, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	picResp, err := xrequest.Req().SetDoNotParseResponse(true).Get(imageURL)
	if err != nil {
		logs.L().Ctx(ctx).Error("get pic from url error", zap.Error(err))
		return
	}

	res = utils.ResizeIMGFromReader(ctx, picResp.RawBody())
	return
}

func checkDBCache(ctx context.Context, musicID string) (imgKey string, err error) {
	ins := query.Q.LarkImg
	res, err := ins.WithContext(ctx).Where(ins.SongID.Eq(musicID)).Find()
	if err != nil {
		logs.L().Ctx(ctx).Error("get lark img from db error", zap.Error(err))
		return
	}
	if len(res) == 0 {
		return "", errors.New("img key not found")
	}
	return res[0].ImgKey, nil
}

func UploadPicAllinOne(ctx context.Context, imageURL string, musicIDInt int, uploadOSS bool) (key string, ossURL string, err error) { // also minio
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.Int("music.id", musicIDInt))
	span.SetAttributes(otel.PreviewAttrs("img_url", imageURL, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	musicID := strconv.Itoa(musicIDInt)
	imgKey, err := checkDBCache(ctx, musicID)
	if err != nil {
		logs.L().Ctx(ctx).Warn("get lark img from db error", zap.String("musicID", musicID))
		// db 缓存未找到，准备resize上传
		var picData []byte
		picData, err = GetAndResizePicFromURL(ctx, imageURL)
		if err != nil {
			logs.L().Ctx(ctx).Error("resize pic from url error", zap.Error(err))
			return
		}

		imgKey, err = Upload2Lark(ctx, musicID, io.NopCloser(bytes.NewReader(picData)))
		if err != nil {
			logs.L().Ctx(ctx).Error("upload pic to lark error", zap.Error(err))
			return
		}
		if uploadOSS {
			dal := miniodal.New(miniodal.Internal)
			res := dal.Upload(ctx).WithContentType(ContentTypeImgJPEG.String()).WithData(picData).Do("cloudmusic", "picture/"+musicID+filepath.Ext(imageURL), minio.PutObjectOptions{})
			if res.Err() != nil {
				logs.L().Ctx(ctx).Warn("upload pic to minio error", zap.String("file_key", "picture/"+musicID+filepath.Ext(imageURL)), zap.String("file_type", ContentTypeImgJPEG.String()))
				return
			}
			u, err := res.PreSignURL()
			if err != nil {
				logs.L().Ctx(ctx).Error("get presign url error", zap.Error(err))
			}
			logs.L().Ctx(ctx).Info("upload pic to minio success", zap.String("file_type", ContentTypeImgJPEG.String()),
				zap.String("url", u))
			ossURL = u
		}
	}
	u, err := miniodal.TryGetFile(ctx, "cloudmusic", "picture/"+musicID+filepath.Ext(imageURL))
	if err != nil {
		logs.L().Ctx(ctx).Warn("get pic from minio error", zap.String("imageURL", imageURL), zap.String("imageKey", imgKey))
		err = nil
	}
	if u != "" {
		ossURL = u
	}
	return imgKey, ossURL, err
}

func Upload2Lark(ctx context.Context, musicID string, bodyReader io.ReadCloser) (imgKey string, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := larkim.NewCreateImageReqBuilder().
		Body(
			larkim.NewCreateImageReqBodyBuilder().
				ImageType(larkim.ImageTypeMessage).
				Image(bodyReader).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Im.Image.Create(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("Error", zap.Error(err))
		return "", nil
	}
	if !resp.Success() {
		return "", errors.New("error with code" + strconv.Itoa(resp.Code))
	}
	imgKey = *resp.Data.ImageKey
	ins := query.Q.LarkImg
	err = ins.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.LarkImg{
		SongID: musicID,
		ImgKey: imgKey,
	})
	if err != nil {
		logs.L().Ctx(ctx).Error("get lark img from db error", zap.Error(err))
		return
	}
	return imgKey, nil
}

func UploadPicture2LarkReader(ctx context.Context, picture io.Reader) (imgKey string) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	req := larkim.NewCreateImageReqBuilder().
		Body(
			larkim.NewCreateImageReqBodyBuilder().
				ImageType(larkim.ImageTypeMessage).
				Image(picture).
				Build(),
		).
		Build()

	resp, err := lark_dal.Client().Im.Image.Create(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("Error", zap.Error(err))
		return
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("error with code" + strconv.Itoa(resp.Code))
		return
	}
	imgKey = *resp.Data.ImageKey
	return imgKey
}

func UploadPicture2Lark(ctx context.Context, URL string) (imgKey string) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	picData, err := GetAndResizePicFromURL(ctx, URL)
	if err != nil {
		logs.L().Ctx(ctx).Error("resize pic from url error", zap.Error(err))
		return
	}

	req := larkim.NewCreateImageReqBuilder().
		Body(
			larkim.NewCreateImageReqBodyBuilder().
				ImageType(larkim.ImageTypeMessage).
				Image(bytes.NewReader(picData)).
				Build(),
		).
		Build()

	resp, err := lark_dal.Client().Im.Image.Create(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("Error", zap.Error(err))
		return
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("error with code" + strconv.Itoa(resp.Code))
		return
	}
	imgKey = *resp.Data.ImageKey
	return imgKey
}

func UploadPicBatch(ctx context.Context, sourceURLIDs map[string]int) chan [2]string {
	var (
		c  = make(chan [2]string, len(sourceURLIDs))
		wg = &sync.WaitGroup{}
	)

	for url, musicID := range sourceURLIDs {
		wg.Add(1)
		go func(url string, musicID int) {
			defer wg.Done()
			_, _, err := UploadPicAllinOne(ctx, url, musicID, true)
			if err != nil {
				logs.L().Ctx(ctx).Error("upload pic to lark error", zap.Error(err))
				return
			}
			c <- [2]string{url, strconv.Itoa(musicID)}
		}(url, musicID)
	}
	go func() {
		wg.Wait()
		close(c)
	}()

	return c
}

func GetMsgImages(ctx context.Context, msgID, fileKey, fileType string) (file io.Reader, err error) {
	req := larkim.NewGetMessageResourceReqBuilder().MessageId(msgID).FileKey(fileKey).Type(fileType).Build()
	resp, err := lark_dal.Client().Im.MessageResource.Get(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("Error", zap.Error(err))
		return nil, err
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("GetMsgImages error with code" + strconv.Itoa(resp.Code))
		return nil, errors.New(resp.Error())
	}
	return resp.File, nil
}

// UploadAudio 上传音频文件到 Lark 并返回 file_key
//
//	@param ctx context.Context
//	@param audioReader 音频文件内容
//	@param fileName 文件名（带后缀，如 "song.mp3"）
//	@param durationMs 音频时长，单位毫秒
//	@return fileKey 文件key，用于发送音频消息
//	@return err error
func UploadAudio(ctx context.Context, audioReader io.Reader, fileName string, durationMs int) (fileKey string, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	bodyBuilder := larkim.NewCreateFileReqBodyBuilder().
		FileType("opus").
		FileName(fileName).
		File(audioReader)
	if durationMs > 0 {
		bodyBuilder = bodyBuilder.Duration(durationMs)
	}

	req := larkim.NewCreateFileReqBuilder().
		Body(bodyBuilder.Build()).
		Build()
	resp, err := lark_dal.Client().Im.File.Create(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("UploadAudio error", zap.Error(err))
		return "", err
	}
	if !resp.Success() {
		return "", errors.New("upload audio failed: " + resp.Error())
	}
	if resp.Data == nil || resp.Data.FileKey == nil {
		return "", errors.New("upload audio returned empty file_key")
	}
	return *resp.Data.FileKey, nil
}

// GetAudioFromURL 从 URL 下载音频文件
func GetAudioFromURL(ctx context.Context, audioURL string) (audioData []byte, err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("audio_url", audioURL, 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	audioResp, err := xrequest.Req().SetDoNotParseResponse(true).Get(audioURL)
	if err != nil {
		logs.L().Ctx(ctx).Error("get audio from url error", zap.Error(err))
		return nil, err
	}

	audioData, err = io.ReadAll(audioResp.RawBody())
	if err != nil {
		logs.L().Ctx(ctx).Error("read audio data error", zap.Error(err))
		return nil, err
	}
	return audioData, nil
}

// ConvertMp3ToOpus 将 mp3 音频转换为 opus 格式（使用 ffmpeg）
//
//	@param ctx context.Context
//	@param mp3Data mp3 音频数据
//	@return opusData opus 音频数据
//	@return durationMs 音频时长，单位毫秒
//	@return err error
func ConvertMp3ToOpus(ctx context.Context, mp3Data []byte) (opusData []byte, durationMs int, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	// 先获取 mp3 时长（不落盘、不依赖 ffprobe；直接解析帧头）
	durationMs, err = probeMp3DurationMs(mp3Data)
	if err != nil {
		logs.L().Ctx(ctx).Warn("probe mp3 duration failed", zap.Error(err))
		durationMs = 0
	}

	// 执行 ffmpeg 转换：stdin 读 mp3，stdout 输出 Ogg Opus
	cmd := exec.CommandContext(ctx,
		ffmpegBin(),
		"-hide_banner",
		"-loglevel", "error",
		"-i", "pipe:0",
		"-c:a", "libopus",
		"-b:a", "128k",
		"-f", "opus",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(mp3Data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	opusData, err = cmd.Output()
	if err != nil {
		logs.L().Ctx(ctx).Error("ffmpeg convert failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return nil, 0, errors.New("ffmpeg 转换失败")
	}

	return opusData, durationMs, nil
}

// ConvertMp3ToOpusAndUpload 将 mp3 直接流式转换为 Ogg Opus 并上传到 Lark
//
// 注意：该实现不落盘；ffmpeg 通过 stdout 直接产生 Ogg Opus 数据，UploadAudio 直接消费 reader。
func ConvertMp3ToOpusAndUpload(ctx context.Context, mp3Data []byte, fileName string) (fileKey string, durationMs int, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	// 先 probe 出时长，供 UploadAudio 填充 duration（不填则无法展示具体时长）
	durationMs, err = probeMp3DurationMs(mp3Data)
	if err != nil {
		logs.L().Ctx(ctx).Warn("probe mp3 duration failed, will upload without duration", zap.Error(err))
		durationMs = 0
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx,
		ffmpegBin(),
		"-hide_banner",
		"-loglevel", "error",
		"-i", "pipe:0",
		"-c:a", "libopus",
		"-b:a", "128k",
		"-f", "opus",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(mp3Data)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logs.L().Ctx(ctx).Error("create ffmpeg stdout pipe failed", zap.Error(err))
		return "", 0, errors.New("创建 ffmpeg 管道失败")
	}
	defer stdout.Close()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		logs.L().Ctx(ctx).Error("start ffmpeg failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return "", 0, errors.New("启动 ffmpeg 失败")
	}

	fileKey, err = UploadAudio(ctx, stdout, fileName, durationMs)
	if err != nil {
		// 上传失败时主动取消 ffmpeg，避免 stdout 无人消费导致 ffmpeg 阻塞
		cancel()
		_ = cmd.Wait()
		return "", durationMs, err
	}

	if err := cmd.Wait(); err != nil {
		logs.L().Ctx(ctx).Error("ffmpeg exited with error", zap.Error(err), zap.String("stderr", stderr.String()))
		return "", durationMs, errors.New("ffmpeg 转换失败")
	}
	return fileKey, durationMs, nil
}

// probeMp3DurationMs 解析 mp3 帧头统计时长（毫秒）。
//
// 说明：ffprobe 对非 seekable 输入（pipe）常返回 N/A；这里用纯内存解析规避该限制。
func probeMp3DurationMs(mp3Data []byte) (int, error) {
	off := 0
	if len(mp3Data) >= 10 && string(mp3Data[:3]) == "ID3" {
		sz := syncsafeInt(mp3Data[6:10])
		off = 10 + sz
		if off > len(mp3Data) {
			off = 0
		}
	}

	var (
		sampleRate    int
		totalSamples  int64
		foundFirstFrm bool
	)
	for i := off; i+4 <= len(mp3Data); {
		// sync word: 11 bits set
		if mp3Data[i] != 0xFF || (mp3Data[i+1]&0xE0) != 0xE0 {
			i++
			continue
		}

		h := uint32(mp3Data[i])<<24 | uint32(mp3Data[i+1])<<16 | uint32(mp3Data[i+2])<<8 | uint32(mp3Data[i+3])
		verID := (h >> 19) & 0x3
		layerID := (h >> 17) & 0x3
		bitrateIdx := (h >> 12) & 0xF
		srIdx := (h >> 10) & 0x3
		padding := int((h >> 9) & 0x1)

		// 仅支持 Layer III；其它 layer 直接跳过
		if layerID != 0x1 {
			i++
			continue
		}

		ver := mpegVersionFromID(verID)
		if ver == mpegVersionInvalid {
			i++
			continue
		}

		sr := sampleRateFrom(ver, srIdx)
		if sr == 0 {
			i++
			continue
		}
		brKbps := bitrateKbpsFrom(ver, bitrateIdx)
		if brKbps == 0 {
			i++
			continue
		}

		frameLen := frameLengthBytes(ver, brKbps, sr, padding)
		if frameLen <= 0 {
			i++
			continue
		}
		if i+frameLen > len(mp3Data) {
			break
		}

		if !foundFirstFrm {
			foundFirstFrm = true
			sampleRate = sr
		} else if sampleRate != sr {
			// 异常：采样率变化，仍按首帧采样率计算（避免返回 0）
		}

		totalSamples += int64(samplesPerFrame(ver))
		i += frameLen
	}

	if !foundFirstFrm || sampleRate == 0 || totalSamples == 0 {
		return 0, errors.New("invalid mp3 data")
	}
	return int(totalSamples * 1000 / int64(sampleRate)), nil
}

type mpegVersion int

const (
	mpegVersionInvalid mpegVersion = iota
	mpegVersion25
	mpegVersion2
	mpegVersion1
)

func mpegVersionFromID(verID uint32) mpegVersion {
	switch verID {
	case 0:
		return mpegVersion25
	case 2:
		return mpegVersion2
	case 3:
		return mpegVersion1
	default:
		return mpegVersionInvalid
	}
}

func sampleRateFrom(ver mpegVersion, srIdx uint32) int {
	if srIdx == 3 {
		return 0
	}
	switch ver {
	case mpegVersion1:
		return []int{44100, 48000, 32000}[srIdx]
	case mpegVersion2:
		return []int{22050, 24000, 16000}[srIdx]
	case mpegVersion25:
		return []int{11025, 12000, 8000}[srIdx]
	default:
		return 0
	}
}

func bitrateKbpsFrom(ver mpegVersion, bitrateIdx uint32) int {
	if bitrateIdx == 0 || bitrateIdx == 15 {
		return 0
	}
	// Layer III bitrate table
	if ver == mpegVersion1 {
		// MPEG1 Layer III
		return []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}[bitrateIdx]
	}
	// MPEG2/2.5 Layer III
	return []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}[bitrateIdx]
}

func frameLengthBytes(ver mpegVersion, bitrateKbps, sampleRate, padding int) int {
	if bitrateKbps <= 0 || sampleRate <= 0 {
		return 0
	}
	if ver == mpegVersion1 {
		// 144 * (bitrate/8) / sampleRate  => 144000 * kbps / sampleRate
		return (144000*bitrateKbps)/sampleRate + padding
	}
	// MPEG2/2.5 Layer III
	return (72000*bitrateKbps)/sampleRate + padding
}

func samplesPerFrame(ver mpegVersion) int {
	if ver == mpegVersion1 {
		return 1152
	}
	return 576
}

func syncsafeInt(b []byte) int {
	// ID3v2 syncsafe integer (4 bytes, 7 bits each)
	if len(b) < 4 {
		return 0
	}
	return int(b[0]&0x7F)<<21 | int(b[1]&0x7F)<<14 | int(b[2]&0x7F)<<7 | int(b[3]&0x7F)
}

func ffmpegBin() string {
	return resolveBin("/usr/bin/ffmpeg", "ffmpeg")
}

func resolveBin(preferredPath, name string) string {
	if preferredPath != "" {
		if _, err := os.Stat(preferredPath); err == nil {
			return preferredPath
		}
	}
	if p, err := exec.LookPath(name); err == nil && p != "" {
		return p
	}
	// 兜底：让 exec 自己按 PATH 解析（或直接失败并返回 stderr）
	return name
}
