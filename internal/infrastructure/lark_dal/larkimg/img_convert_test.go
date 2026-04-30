package larkimg

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestConvertMp3ToOpus(t *testing.T) {
	ffmpeg := ffmpegBin()
	if err := exec.Command(ffmpeg, "-version").Run(); err != nil {
		t.Skipf("ffmpeg 不可用，跳过: %v", err)
	}

	mp3Data, err := genTestMp3(t, ffmpeg, 200*time.Millisecond)
	if err != nil {
		t.Skipf("无法生成测试 mp3（可能缺少编码器）：%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opusData, durationMs, err := ConvertMp3ToOpus(ctx, mp3Data)
	if err != nil {
		t.Fatalf("ConvertMp3ToOpus 返回错误: %v", err)
	}
	if len(opusData) == 0 {
		t.Fatalf("opusData 为空")
	}
	if durationMs <= 0 {
		t.Fatalf("durationMs 非法: %d", durationMs)
	}
	// 允许一定误差（ffprobe / 过滤器边界可能产生偏差）
	if durationMs < 100 || durationMs > 2000 {
		t.Fatalf("durationMs 超出预期范围: %d", durationMs)
	}
}

func BenchmarkConvertMp3ToOpus_Stream(b *testing.B) {
	mp3Data := loadBenchMp3(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(mp3Data)))

	for i := 0; i < b.N; i++ {
		opusData, durationMs, err := ConvertMp3ToOpus(ctx, mp3Data)
		if err != nil {
			b.Fatalf("ConvertMp3ToOpus error: %v", err)
		}
		if len(opusData) == 0 || durationMs <= 0 {
			b.Fatalf("invalid result: opus=%d duration=%d", len(opusData), durationMs)
		}
	}
}

func BenchmarkConvertMp3ToOpus_Tempfile(b *testing.B) {
	mp3Data := loadBenchMp3(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(mp3Data)))

	for i := 0; i < b.N; i++ {
		opusData, durationMs, err := convertMp3ToOpusTempfile(ctx, mp3Data)
		if err != nil {
			b.Fatalf("convertMp3ToOpusTempfile error: %v", err)
		}
		if len(opusData) == 0 || durationMs <= 0 {
			b.Fatalf("invalid result: opus=%d duration=%d", len(opusData), durationMs)
		}
	}
}

// BenchmarkConvertMp3ToOpusAndUpload_*：把“上传阶段”也纳入对比。
//
// 说明：真实 UploadAudio 会走网络与鉴权，不适合在 benchmark 中跑；这里用 io.Copy(io.Discard, reader)
// 来模拟“飞书 SDK 读取 io.Reader 上传”的消费行为，从而把 reader/pipe/磁盘读取等开销纳入。
func BenchmarkConvertMp3ToOpusAndUpload_StreamDiscard(b *testing.B) {
	mp3Data := loadBenchMp3(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(mp3Data)))

	for i := 0; i < b.N; i++ {
		durationMs, err := convertMp3ToOggOpusStreamToWriter(ctx, mp3Data, io.Discard)
		if err != nil {
			b.Fatalf("convertMp3ToOggOpusStreamToWriter error: %v", err)
		}
		if durationMs <= 0 {
			b.Fatalf("invalid duration: %d", durationMs)
		}
	}
}

func BenchmarkConvertMp3ToOpusAndUpload_TempfileDiscard(b *testing.B) {
	mp3Data := loadBenchMp3(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(mp3Data)))

	for i := 0; i < b.N; i++ {
		durationMs, err := convertMp3ToOggOpusTempfileToWriter(ctx, mp3Data, io.Discard)
		if err != nil {
			b.Fatalf("convertMp3ToOggOpusTempfileToWriter error: %v", err)
		}
		if durationMs <= 0 {
			b.Fatalf("invalid duration: %d", durationMs)
		}
	}
}

func genTestMp3(t *testing.T, ffmpeg string, dur time.Duration) ([]byte, error) {
	t.Helper()

	seconds := float64(dur) / float64(time.Second)
	args1 := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "sine=frequency=1000:sample_rate=44100",
		"-t", formatFloatSeconds(seconds),
		"-ac", "1",
		"-c:a", "libmp3lame",
		"-q:a", "5",
		"-f", "mp3",
		"pipe:1",
	}
	if out, err := exec.Command(ffmpeg, args1...).Output(); err == nil && len(out) > 0 {
		return out, nil
	}

	// fallback：某些 ffmpeg 构建可能没有 libmp3lame
	args2 := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "sine=frequency=1000:sample_rate=44100",
		"-t", formatFloatSeconds(seconds),
		"-ac", "1",
		"-c:a", "mp3",
		"-f", "mp3",
		"pipe:1",
	}
	return exec.Command(ffmpeg, args2...).Output()
}

func loadBenchMp3(b *testing.B) []byte {
	b.Helper()

	// 由你提供原始 mp3 文件路径
	path := os.Getenv("BETAGO_BENCH_MP3")
	if path == "" {
		path = os.Getenv("BENCH_MP3")
	}
	if path == "" {
		b.Skip("未设置 mp3 路径：请设置环境变量 BETAGO_BENCH_MP3=/path/to/file.mp3")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatalf("read mp3 failed: %v", err)
	}
	if len(data) == 0 {
		b.Fatalf("mp3 file is empty: %s", path)
	}
	return data
}

// convertMp3ToOpusTempfile 模拟旧实现：临时文件落盘 + ffprobe(file) + ffmpeg(file->file) + 读回内存。
// 仅用于对比 Benchmark。
func convertMp3ToOpusTempfile(ctx context.Context, mp3Data []byte) (opusData []byte, durationMs int, err error) {
	tmpDir := os.TempDir()
	mp3File, err := os.CreateTemp(tmpDir, "betago_bench_*.mp3")
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(mp3File.Name())
	defer mp3File.Close()

	opusFile, err := os.CreateTemp(tmpDir, "betago_bench_*.opus")
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(opusFile.Name())
	defer opusFile.Close()

	if _, err := mp3File.Write(mp3Data); err != nil {
		return nil, 0, err
	}
	_ = mp3File.Close()

	durationMs, _ = getAudioDurationFile(ctx, mp3File.Name())

	cmd := exec.CommandContext(ctx, ffmpegBin(), "-y", "-i", mp3File.Name(), "-c:a", "libopus", "-b:a", "128k", opusFile.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, 0, errors.New("ffmpeg 转换失败: " + string(out))
	}

	if durationMs == 0 {
		durationMs, _ = getAudioDurationFile(ctx, opusFile.Name())
	}
	opusData, err = os.ReadFile(opusFile.Name())
	if err != nil {
		return nil, 0, err
	}
	return opusData, durationMs, nil
}

func convertMp3ToOggOpusStreamToWriter(ctx context.Context, mp3Data []byte, dst io.Writer) (durationMs int, err error) {
	// 用生产逻辑同样的时长探测（mp3 帧头解析）
	durationMs, err = probeMp3DurationMs(mp3Data)
	if err != nil {
		return 0, err
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
	cmd.Stdin = bytesReader(mp3Data)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	if _, err := io.Copy(dst, stdout); err != nil {
		cancel()
		_ = cmd.Wait()
		return 0, err
	}
	if err := cmd.Wait(); err != nil {
		return 0, err
	}
	return durationMs, nil
}

func convertMp3ToOggOpusTempfileToWriter(ctx context.Context, mp3Data []byte, dst io.Writer) (durationMs int, err error) {
	tmpDir := os.TempDir()
	mp3File, err := os.CreateTemp(tmpDir, "betago_bench_*.mp3")
	if err != nil {
		return 0, err
	}
	defer os.Remove(mp3File.Name())
	defer mp3File.Close()

	opusFile, err := os.CreateTemp(tmpDir, "betago_bench_*.opus")
	if err != nil {
		return 0, err
	}
	defer os.Remove(opusFile.Name())
	defer opusFile.Close()

	if _, err := mp3File.Write(mp3Data); err != nil {
		return 0, err
	}
	_ = mp3File.Close()

	// 旧逻辑：probe 文件时长（可选）
	durationMs, _ = getAudioDurationFile(ctx, mp3File.Name())

	cmd := exec.CommandContext(ctx, ffmpegBin(), "-y", "-i", mp3File.Name(), "-c:a", "libopus", "-b:a", "128k", opusFile.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, errors.New("ffmpeg 转换失败: " + string(out))
	}

	if durationMs == 0 {
		durationMs, _ = getAudioDurationFile(ctx, opusFile.Name())
	}

	f, err := os.Open(opusFile.Name())
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := io.Copy(dst, f); err != nil {
		return 0, err
	}
	return durationMs, nil
}

func bytesReader(b []byte) io.Reader {
	// 避免额外引入 bytes 包；这里只要一个从头读的 reader。
	return &sliceReader{b: b}
}

type sliceReader struct {
	b []byte
	i int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func getAudioDurationFile(ctx context.Context, filePath string) (int, error) {
	ffprobe := resolveTestBin("/usr/bin/ffprobe", "ffprobe")
	cmd := exec.CommandContext(ctx,
		ffprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}
	var duration float64
	if _, err := fmtSscanfFloat(string(output), &duration); err != nil {
		return 0, err
	}
	return int(duration * 1000), nil
}

func resolveTestBin(preferredPath, name string) string {
	if preferredPath != "" {
		if _, err := os.Stat(preferredPath); err == nil {
			return preferredPath
		}
	}
	if p, err := exec.LookPath(name); err == nil && p != "" {
		return p
	}
	return name
}

// fmtSscanfFloat 避免从生产代码 import fmt；这里只需要解析 float。
func fmtSscanfFloat(s string, out *float64) (int, error) {
	// ffprobe 返回形如 "0.200000\n"
	trim := s
	for len(trim) > 0 {
		c := trim[len(trim)-1]
		if c == '\n' || c == '\r' || c == ' ' || c == '\t' {
			trim = trim[:len(trim)-1]
			continue
		}
		break
	}
	// 这里只处理十进制浮点
	var (
		sign   = 1.0
		val    = 0.0
		frac   = 0.0
		div    = 1.0
		inFrac = false
	)
	if trim == "" {
		return 0, errors.New("empty duration")
	}
	if trim[0] == '-' {
		sign = -1
		trim = trim[1:]
	}
	if trim == "" {
		return 0, errors.New("invalid duration")
	}
	for i := 0; i < len(trim); i++ {
		c := trim[i]
		if c == '.' {
			if inFrac {
				return 0, errors.New("invalid duration")
			}
			inFrac = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, errors.New("invalid duration")
		}
		d := float64(c - '0')
		if !inFrac {
			val = val*10 + d
		} else {
			div *= 10
			frac += d / div
		}
	}
	*out = sign * (val + frac)
	return 1, nil
}

func formatFloatSeconds(v float64) string {
	// 避免引入 fmt/sprintf 的额外依赖；这里用固定小数位足够。
	// 例如 0.2s -> "0.200"
	ms := int(v*1000 + 0.5)
	sec := ms / 1000
	rem := ms % 1000
	return itoa(sec) + "." + itoa3(rem)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	s := make([]byte, 0, 11)
	n := v
	for n > 0 {
		d := n % 10
		s = append(s, byte('0'+d))
		n /= 10
	}
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return string(s)
}

func itoa3(v int) string {
	// v in [0,999]
	b := [3]byte{'0', '0', '0'}
	b[0] = byte('0' + (v/100)%10)
	b[1] = byte('0' + (v/10)%10)
	b[2] = byte('0' + v%10)
	return string(b[:])
}
