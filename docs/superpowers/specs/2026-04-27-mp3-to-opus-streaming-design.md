# MP3 → Ogg Opus 流式转换设计方案

## 背景

当前 `ConvertMp3ToOpus` 使用 ffmpeg 进程 + 临时文件中转，性能开销大。需求是用纯内存流式方案替代。

## 目标

- 消除磁盘 I/O（无临时文件）
- 消除进程 fork（无 ffmpeg 依赖）
- 端到端流式处理，内存占用恒定
- 合并 `ConvertMp3ToOpus` + `UploadAudio` 为单一安全操作

## 设计

### 合并后的函数签名

```go
// ConvertMp3ToOpusAndUpload 将 mp3 流式转换并直接上传到 Lark
func ConvertMp3ToOpusAndUpload(ctx context.Context, mp3Data []byte, fileName string) (fileKey string, durationMs int, err error)
```

### Pipeline

```
mp3Data ([]byte)
    ↓
bytes.Reader (as io.Reader)
    ↓
minimp3.Decoder (流式 yield，每帧 1152 samples)
    ↓
opus.Encoder.Encode() (逐帧编码)
    ↓
OggEncoder.Write() (流式写 PipeWriter)
    ↓
Lark UploadAudio (PipeReader 直接消费 Ogg 流)
    ↓
返回 fileKey, durationMs
```

### 关键实现细节

**Ogg 容器封装：** 使用 `github.com/hearista/opus` 的 `OggEncoder`，无需额外依赖。

**Duration 获取：** goroutine 中累积总 sample 数，计算后通过 channel 传回主线程：
```go
totalSamples += len(frame.Data)
durationMs = totalSamples * 1000 / frame.Hz
```

**错误传播：** Pipe 连接编码输出和上传输入，任何阶段失败（MP3 解码错误、Opus 编码错误、上传失败）都会断开 pipe，context cancelled 时立即终止。

**调用方适配：** 移除原来的两处 `ConvertMp3ToOpus` + `UploadAudio` 组合调用，改为直接调用 `ConvertMp3ToOpusAndUpload`。

### 依赖

| 库 | 作用 |
|---|---|
| `github.com/tosone/minimp3` | 纯 Go MP3 流式解码 |
| `github.com/hearista/opus` | libopus C 绑定 + Ogg 封装 |

**系统依赖：** `libopus-dev`, `libogg-dev`

### 改动范围

| 文件 | 改动 |
|---|---|
| `internal/infrastructure/lark_dal/larkimg/img.go` | 新增 `ConvertMp3ToOpusAndUpload`，保留原 `ConvertMp3ToOpus` 签名（内部调用新实现） |
| `internal/application/lark/handlers/music_handler.go` | 替换调用方式 |
| `internal/application/lark/card_handlers/handler.go` | 替换调用方式 |
| `go.mod` | 新增 `tosone/minimp3`、`heyarista/opus` 依赖 |

### 兼容性

- 原 `ConvertMp3ToOpus` 签名保持不变，内部实现替换为流式版本
- 调用方改动仅限替换为新函数，参数类型不变
