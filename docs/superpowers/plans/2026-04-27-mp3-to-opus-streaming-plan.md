# MP3 → Ogg Opus Streaming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace ffmpeg-based MP3→Opus conversion with pure Go+C memory-streaming pipeline, merging `ConvertMp3ToOpus`+`UploadAudio` into one safe operation.

**Architecture:** 流式 pipeline via `io.Pipe()`: minimp3 流式解码 → libopus 编码 → Ogg 封装 → 直接上传 Lark，无磁盘 I/O，无进程 fork。

**Tech Stack:** `github.com/tosone/minimp3`, `github.com/hearista/opus` (libopus C binding)

---

## File Map

| File | Role |
|---|---|
| `internal/infrastructure/lark_dal/larkimg/img.go` | 新增 `ConvertMp3ToOpusAndUpload`；旧 `ConvertMp3ToOpus` 保留但内部调用新函数 |
| `internal/application/lark/handlers/music_handler.go:163-170` | 替换为 `ConvertMp3ToOpusAndUpload` |
| `internal/application/lark/card_handlers/handler.go:155-157` | 替换为 `ConvertMp3ToOpusAndUpload` |
| `go.mod` | 新增 `tosone/minimp3`、`heyarista/opus` 依赖 |

---

## Task 1: Add Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Run go get**

```bash
cd /mnt/RapidPool/workspace/BetaGo_v2
go get github.com/tosone/minimp3@latest github.com/hearista/opus@latest
```

- [ ] **Step 2: Run go mod tidy**

```bash
go mod tidy
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add minimp3 and opus libraries for streaming audio conversion

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: Add ConvertMp3ToOpusAndUpload function

**Files:**
- Modify: `internal/infrastructure/lark_dal/larkimg/img.go`

- [ ] **Step 1: Add import block**

In `img.go`, add to the import block:
```go
import (
    // ... existing imports ...
    "github.com/hearista/opus"
    "github.com/tosone/minimp3"
)
```

- [ ] **Step 2: Add ConvertMp3ToOpusAndUpload function**

Add this function before `ConvertMp3ToOpus` (around line 673):

```go
// ConvertMp3ToOpusAndUpload 将 mp3 流式转换并直接上传到 Lark
// 全程内存流式处理，无磁盘 I/O，无 ffmpeg 进程依赖
func ConvertMp3ToOpusAndUpload(ctx context.Context, mp3Data []byte, fileName string) (fileKey string, durationMs int, err error) {
    ctx, span := otel.Start(ctx)
    defer span.End()
    defer func() { otel.RecordError(span, err) }()

    pr, pw := io.Pipe()
    done := make(chan struct{})
    var encodeErr error

    // 启动流式编码+上传 goroutine
    go func() {
        defer close(done)
        defer pw.Close()

        // 创建 MP3 流式解码器
        decoder, err := minimp3.NewDecoderWithByteReader(bytes.NewReader(mp3Data))
        if err != nil {
            encodeErr = fmt.Errorf("create mp3 decoder failed: %w", err)
            return
        }

        // 创建 Opus 编码器
        sampleRate := 48000
        channels := 2
        enc, err := opus.NewEncoder(sampleRate, channels, opus.ApplicationAudio)
        if err != nil {
            encodeErr = fmt.Errorf("create opus encoder failed: %w", err)
            return
        }

        // 创建 Ogg 封装器
        oggEnc := opus.NewOggEncoder(enc, pw, 0)

        var totalSamples int
        for {
            frame, err := decoder.Read()
            if err == io.EOF {
                break
            }
            if err != nil {
                encodeErr = fmt.Errorf("mp3 decode failed: %w", err)
                return
            }

            pkt, err := enc.Encode(frame.Data, frame.Hz)
            if err != nil {
                encodeErr = fmt.Errorf("opus encode failed: %w", err)
                return
            }

            if err := oggEnc.Write(pkt, 0, 0); err != nil {
                encodeErr = fmt.Errorf("ogg write failed: %w", err)
                return
            }
            totalSamples += len(frame.Data)
        }

        if err := oggEnc.Flush(); err != nil {
            encodeErr = fmt.Errorf("ogg flush failed: %w", err)
            return
        }

        durationMs = totalSamples * 1000 / sampleRate
    }()

    // 上传到 Lark（消费 pipe 输出）
    fileKey, err = UploadAudio(ctx, pr, fileName, durationMs)
    if err != nil {
        return "", 0, fmt.Errorf("upload audio failed: %w", err)
    }

    <-done
    if encodeErr != nil {
        return "", 0, encodeErr
    }

    return fileKey, durationMs, nil
}
```

- [ ] **Step 3: Verify go build passes**

```bash
cd /mnt/RapidPool/workspace/BetaGo_v2 && go build ./...
```

Expected: BUILD SUCCESS (no output)

- [ ] **Step 4: Commit**

```bash
git add internal/infrastructure/lark_dal/larkimg/img.go
git commit -m "feat: add streaming MP3→Opus conversion with direct Lark upload

- Add ConvertMp3ToOpusAndUpload with pure Go+C pipeline
- minimp3 streaming decode → libopus encode → Ogg封装 → direct upload
- Replaces ffmpeg temp file approach

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Update music_handler.go call site

**Files:**
- Modify: `internal/application/lark/handlers/music_handler.go:155-174`

- [ ] **Step 1: Read surrounding context**

```bash
sed -n '150,180p' /mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/music_handler.go
```

- [ ] **Step 2: Replace two-step pattern with single call**

The current code (lines 155-174):
```go
    audioData, err := larkimg.GetAudioFromURL(ctx, song.SongURL)
    if err != nil {
        logs.L().Ctx(ctx).Error("download audio failed", zap.Error(err))
        return err
    }

    // 转换为 opus 格式（飞书语音消息需要 opus）
    opusData, durationMs, err := larkimg.ConvertMp3ToOpus(ctx, audioData)
    if err != nil {
        logs.L().Ctx(ctx).Error("convert to opus failed", zap.Error(err))
        return err
    }

    // 上传到 Lark
    fileKey, err := larkimg.UploadAudio(ctx, bytes.NewReader(opusData), song.Name+".opus", durationMs)
    if err != nil {
        logs.L().Ctx(ctx).Error("upload audio to lark failed", zap.Error(err))
        return err
    }
```

Replace with:
```go
    // 转换为 opus 格式并上传到 Lark（流式处理，无磁盘 I/O）
    fileKey, _, err := larkimg.ConvertMp3ToOpusAndUpload(ctx, audioData, song.Name+".opus")
    if err != nil {
        logs.L().Ctx(ctx).Error("convert and upload audio failed", zap.Error(err))
        return err
    }
```

- [ ] **Step 3: Remove unused bytes import if no longer needed**

After the change, check if `bytes` is still used elsewhere in the file:
```bash
grep -n "bytes\." /mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/music_handler.go
```
If only `bytes.NewReader` remains and that line is gone, remove `"bytes"` from imports.

- [ ] **Step 4: Verify go build**

```bash
cd /mnt/RapidPool/workspace/BetaGo_v2 && go build ./...
```

Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/handlers/music_handler.go
git commit -m "refactor: use ConvertMp3ToOpusAndUpload in music_handler

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: Update card_handlers/handler.go call site

**Files:**
- Modify: `internal/application/lark/card_handlers/handler.go:153-167`

- [ ] **Step 1: Read surrounding context**

```bash
sed -n '145,175p' /mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/card_handlers/handler.go
```

- [ ] **Step 2: Replace two-step pattern with single call**

The current code (lines 152-167):
```go
    audioFileKey := ""
    audioData, err := larkimg.GetAudioFromURL(ctx, musicURL)
    if err == nil {
        opusData, durationMs, err := larkimg.ConvertMp3ToOpus(ctx, audioData)
        if err == nil {
            fileKey, err := larkimg.UploadAudio(ctx, bytes.NewReader(opusData), songDetail.Name+".opus", durationMs)
            if err == nil {
                audioFileKey = fileKey
            } else {
                logs.L().Ctx(ctx).Warn("upload audio failed", zap.Error(err))
            }
        } else {
            logs.L().Ctx(ctx).Warn("convert to opus failed", zap.Error(err))
        }
    } else {
        logs.L().Ctx(ctx).Warn("get audio from url failed", zap.Error(err))
    }
```

Replace with:
```go
    audioFileKey := ""
    audioData, err := larkimg.GetAudioFromURL(ctx, musicURL)
    if err == nil {
        fileKey, _, err := larkimg.ConvertMp3ToOpusAndUpload(ctx, audioData, songDetail.Name+".opus")
        if err == nil {
            audioFileKey = fileKey
        } else {
            logs.L().Ctx(ctx).Warn("convert and upload audio failed", zap.Error(err))
        }
    } else {
        logs.L().Ctx(ctx).Warn("get audio from url failed", zap.Error(err))
    }
```

- [ ] **Step 3: Remove unused bytes import if no longer needed**

```bash
grep -n "bytes\." /mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/card_handlers/handler.go
```
If `bytes.NewReader` is gone, remove `"bytes"` from imports.

- [ ] **Step 4: Verify go build**

```bash
cd /mnt/RapidPool/workspace/BetaGo_v2 && go build ./...
```

Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/card_handlers/handler.go
git commit -m "refactor: use ConvertMp3ToOpusAndUpload in card_handlers

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: Final verification

- [ ] **Step 1: Full build check**

```bash
cd /mnt/RapidPool/workspace/BetaGo_v2 && go build ./...
```

- [ ] **Step 2: Verify no ffmpeg references remain in img.go**

```bash
grep -n "ffmpeg\|ffprobe\|exec.Command" /mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkimg/img.go
```

Expected: no output (the old ffmpeg-based implementation has been replaced)

- [ ] **Step 3: Verify all old ConvertMp3ToOpus callers updated**

```bash
grep -rn "ConvertMp3ToOpus\|UploadAudio" /mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/
```

Expected: only `ConvertMp3ToOpusAndUpload` calls remain, no raw `UploadAudio` calls
