package music

import (
	"bytes"
	"testing"
)

// These are format fixtures, not playback evidence or synthesized real songs.
func durationTestFrames(version, sampleIndex, count int, variable, crc bool) []byte {
	rates := []int{44100, 48000, 32000}
	rate := rates[sampleIndex]
	bitrates := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	coefficient := 144000
	if version != 3 {
		bitrates = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		coefficient = 72000
		rate /= 2
		if version == 0 {
			rate /= 2
		}
	}
	var audio []byte
	for i := 0; i < count; i++ {
		bitrateIndex, padding := 9, 0
		if variable {
			bitrateIndex = 1 + i%14
			padding = i % 2
		}
		frame := make([]byte, coefficient*bitrates[bitrateIndex]/rate+padding)
		frame[0], frame[1] = 0xff, byte(0xe0|version<<3|1<<1|1)
		if crc {
			frame[1] &^= 1
		}
		frame[2] = byte(bitrateIndex<<4 | sampleIndex<<2 | padding<<1)
		frame[3] = 0x64
		audio = append(audio, frame...)
	}
	return audio
}

func durationTestID3(audio []byte, footer bool) []byte {
	size := 130 // Exercises synchsafe sizes beyond one byte.
	header := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, byte(size >> 7), byte(size & 0x7f)}
	if footer {
		header[5] = 0x10
	}
	result := append(append([]byte{}, header...), make([]byte, size)...)
	if footer {
		trailer := append([]byte{'3', 'D', 'I'}, header[3:]...)
		result = append(result, trailer...)
	}
	return append(result, audio...)
}

func TestMP3DurationAcceptsCompleteFrameStreams(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		version, rateIndex      int
		variable, crc, id3, tag bool
		want                    int64
	}{
		{"MPEG1 CBR", 3, 0, false, false, false, false, 26122},
		{"MPEG1 VBR with padding and CRC", 3, 0, true, true, false, false, 26122},
		{"MPEG2", 2, 0, false, false, false, false, 26122},
		{"MPEG2.5", 0, 0, true, false, false, false, 52244},
		{"48kHz", 3, 1, false, false, false, false, 24000},
		{"32kHz", 3, 2, false, false, false, false, 36000},
		{"ID3v2 footer and ID3v1", 3, 0, false, false, true, true, 26122},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audio := durationTestFrames(tc.version, tc.rateIndex, 1000, tc.variable, tc.crc)
			if tc.id3 {
				audio = durationTestID3(audio, true)
			}
			if tc.tag {
				audio = append(audio, append([]byte("TAG"), make([]byte, 125)...)...)
			}
			got, ok := validateCompleteMP3Duration(audio, tc.want)
			if !ok || got != tc.want {
				t.Fatalf("duration=%d ok=%v, want %d and complete", got, ok, tc.want)
			}
		})
	}
}

func TestMP3DurationRejectsIncompleteOrMalformedStreams(t *testing.T) {
	valid := durationTestFrames(3, 0, 100, false, false)
	mutate := func(offset int, value byte) []byte {
		b := bytes.Clone(valid)
		b[offset] = value
		return b
	}
	footer := durationTestID3(valid, true)
	badFooter := bytes.Clone(footer)
	badFooter[140] = 'X'
	badTagSize := durationTestID3(valid, false)
	badTagSize[6] = 0x80
	for _, tc := range []struct {
		name  string
		audio []byte
	}{
		{"empty", nil},
		{"header only", valid[:4]},
		{"one complete frame", durationTestFrames(3, 0, 1, false, false)},
		{"ID3 and one frame", durationTestID3(durationTestFrames(3, 0, 1, false, false), false)},
		{"truncated last frame", valid[:len(valid)-1]},
		{"trailing partial header", append(bytes.Clone(valid), 0xff, 0xfb)},
		{"trailing garbage", append(bytes.Clone(valid), 0, 0, 0)},
		{"leading garbage", append([]byte{0}, valid...)},
		{"reserved version", mutate(1, 0xeb)},
		{"wrong layer", mutate(1, 0xfd)},
		{"free bitrate", mutate(2, 0x00)},
		{"invalid bitrate", mutate(2, 0xf0)},
		{"reserved sample rate", mutate(2, 0x9c)},
		{"reserved emphasis", mutate(3, 0x66)},
		{"changed sample rate", append(bytes.Clone(valid), durationTestFrames(3, 1, 2, false, false)...)},
		{"changed MPEG version", append(bytes.Clone(valid), durationTestFrames(2, 0, 2, false, false)...)},
		{"ID3 short header", []byte("ID3\x04\x00")},
		{"ID3 oversized body", []byte("ID3\x04\x00\x00\x7f\x7f\x7f\x7f")},
		{"ID3 invalid synchsafe size", badTagSize},
		{"ID3 missing footer", append(bytes.Clone(footer[:140]), valid...)},
		{"ID3 invalid footer", badFooter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := validateCompleteMP3Duration(tc.audio, 2612); ok {
				t.Fatal("accepted incomplete or malformed MP3")
			}
		})
	}
}

func TestMP3DurationRequiresCanonicalDurationAndBoundedTolerance(t *testing.T) {
	audio := durationTestFrames(3, 0, 1000, false, false) // 26122ms
	for _, tc := range []struct {
		name      string
		canonical int64
		want      bool
	}{
		{"exact", 26122, true},
		{"small encoder difference", 26500, true},
		{"minimum tolerance accepted", 24622, true},
		{"minimum tolerance exceeded", 24621, false},
		{"missing", 0, false},
		{"negative", -1, false},
		{"excerpt cannot be full", 326000, false},
		{"overflow size canonical", 1<<63 - 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := validateCompleteMP3Duration(audio, tc.canonical)
			if ok != tc.want {
				t.Fatalf("ok=%v, want %v", ok, tc.want)
			}
		})
	}
	long := durationTestFrames(3, 0, 15313, false, false) // 400013ms
	if _, ok := validateCompleteMP3Duration(long, 395013); !ok {
		t.Fatal("rejected the allowed five-second difference")
	}
	if _, ok := validateCompleteMP3Duration(long, 394013); ok {
		t.Fatal("accepted six-second difference through uncapped three-percent tolerance")
	}
	preview := durationTestFrames(3, 0, 420, false, false) // 10971ms
	if _, ok := validateCompleteMP3Duration(preview, 326000); ok {
		t.Fatal("accepted eleven-second preview as full track")
	}
	medium := durationTestFrames(3, 0, 3830, false, false) // 100048ms
	if _, ok := validateCompleteMP3Duration(medium, 98048); !ok {
		t.Fatal("rejected a two-percent duration difference")
	}
	if _, ok := validateCompleteMP3Duration(medium, 97048); ok {
		t.Fatal("accepted a duration difference above three percent")
	}
}

func TestMP3DurationRequiresTwoFramesEvenWhenCanonicalMatches(t *testing.T) {
	if _, ok := validateCompleteMP3Duration(durationTestFrames(3, 0, 1, false, false), 26); ok {
		t.Fatal("accepted a single frame with matching canonical duration")
	}
}

func TestMP3DurationSkipsID3v2WithoutFooter(t *testing.T) {
	for _, version := range []byte{2, 3, 4} {
		audio := durationTestID3(durationTestFrames(3, 0, 1000, false, false), false)
		audio[3] = version
		if got, ok := validateCompleteMP3Duration(audio, 26122); !ok || got != 26122 {
			t.Fatalf("ID3v2.%d: duration=%d complete=%v", version, got, ok)
		}
	}
}
