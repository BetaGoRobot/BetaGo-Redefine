package music

import "bytes"

// validateCompleteMP3Duration checks the complete Layer III frame envelope and
// compares its sample duration with trusted catalogue metadata. It is not a
// decoder, recording-identity proof, or a validation of compressed audio/CRC.
func validateCompleteMP3Duration(audio []byte, canonicalMs int64) (durationMs int64, ok bool) {
	if canonicalMs <= 0 {
		return 0, false
	}
	start, end := 0, len(audio)
	if len(audio) >= 3 && bytes.Equal(audio[:3], []byte("ID3")) {
		if len(audio) < 10 || audio[3] < 2 || audio[3] > 4 || audio[4] == 0xff {
			return 0, false
		}
		allowedFlags := byte(0xc0)
		if audio[3] == 3 {
			allowedFlags = 0xe0
		} else if audio[3] == 4 {
			allowedFlags = 0xf0
		}
		if audio[5] & ^allowedFlags != 0 {
			return 0, false
		}
		tagSize := 0
		for _, b := range audio[6:10] {
			if b&0x80 != 0 {
				return 0, false
			}
			tagSize = tagSize<<7 | int(b)
		}
		if tagSize > end-10 {
			return 0, false
		}
		start = 10 + tagSize
		// ID3v2.4's synchsafe size excludes both header and optional footer.
		if audio[3] == 4 && audio[5]&0x10 != 0 {
			if end-start < 10 || !bytes.Equal(audio[start:start+3], []byte("3DI")) ||
				!bytes.Equal(audio[start+3:start+10], audio[3:10]) {
				return 0, false
			}
			start += 10
		}
	}
	if end-start >= 128 && bytes.Equal(audio[end-128:end-125], []byte("TAG")) {
		end -= 128
	}
	bitratesMPEG1 := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	bitratesLSF := [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	baseRates := [...]int{44100, 48000, 32000}
	frames, streamVersion, streamRate := 0, -1, 0
	var totalSamples int64
	for pos := start; pos < end; {
		if end-pos < 4 {
			return 0, false
		}
		h := audio[pos : pos+4]
		version, layer := int(h[1]>>3&3), h[1]>>1&3
		bitrateIndex, rateIndex := int(h[2]>>4), int(h[2]>>2&3)
		if h[0] != 0xff || h[1]&0xe0 != 0xe0 || version == 1 || layer != 1 ||
			bitrateIndex == 0 || bitrateIndex == 15 || rateIndex == 3 || h[3]&3 == 2 {
			return 0, false
		}
		rate, bitrate, coefficient, samples := baseRates[rateIndex], bitratesMPEG1[bitrateIndex], 144000, 1152
		sideInfo := 32
		if h[3]>>6 == 3 {
			sideInfo = 17
		}
		if version != 3 {
			rate /= 2
			if version == 0 {
				rate /= 2
			}
			bitrate, coefficient, samples = bitratesLSF[bitrateIndex], 72000, 576
			sideInfo = 17
			if h[3]>>6 == 3 {
				sideInfo = 9
			}
		}
		if frames != 0 && (version != streamVersion || rate != streamRate) {
			return 0, false
		}
		frameSize := coefficient*bitrate/rate + int(h[2]>>1&1)
		minimumSize := 4 + sideInfo
		if h[1]&1 == 0 {
			minimumSize += 2 // CRC protection bytes occupy space within the frame.
		}
		if frameSize < minimumSize || frameSize > end-pos {
			return 0, false
		}
		streamVersion, streamRate = version, rate
		frames++
		totalSamples += int64(samples)
		pos += frameSize
	}
	if frames < 2 {
		return 0, false
	}
	durationMs = totalSamples * 1000 / int64(streamRate)
	// Calculate three percent without overflowing for hostile catalogue input.
	tolerance := canonicalMs/100*3 + canonicalMs%100*3/100
	if tolerance < 1500 {
		tolerance = 1500
	}
	if tolerance > 5000 {
		tolerance = 5000
	}
	difference := durationMs - canonicalMs
	if difference < 0 {
		difference = -difference
	}
	return durationMs, difference <= tolerance
}
