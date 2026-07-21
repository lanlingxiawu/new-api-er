package common

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildWAV creates a minimal valid PCM WAV in memory:
// sampleRate*numChannels frames of silence => duration == frames/sampleRate.
func buildWAV(sampleRate, numChannels, bitsPerSample, numFrames int) []byte {
	bytesPerFrame := numChannels * bitsPerSample / 8
	dataSize := numFrames * bytesPerFrame
	byteRate := sampleRate * bytesPerFrame

	buf := new(bytes.Buffer)
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(buf, binary.LittleEndian, uint16(bytesPerFrame))
	binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

func TestGetAudioDuration_WAV(t *testing.T) {
	// 8000 frames at 8000 Hz => 1.0 second
	wav := buildWAV(8000, 1, 16, 8000)
	d, err := GetAudioDuration(context.Background(), bytes.NewReader(wav), ".wav")
	require.NoError(t, err)
	assert.InDelta(t, 1.0, d, 0.01)
}

func TestGetAudioDuration_Unsupported(t *testing.T) {
	_, err := GetAudioDuration(context.Background(), bytes.NewReader([]byte("x")), ".xyz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported audio format")
}

func TestGetWAVDuration_Invalid(t *testing.T) {
	_, err := getWAVDuration(bytes.NewReader([]byte("not a wav file at all")))
	require.Error(t, err)
}

func TestGetFLACDuration_Invalid(t *testing.T) {
	_, err := getFLACDuration(bytes.NewReader([]byte("not flac")))
	require.Error(t, err)
}

func TestGetM4ADuration_GarbageTolerated(t *testing.T) {
	// go-mp4's Probe is lenient: it returns no error for trivial/empty input
	// (yielding Timescale 0 => NaN duration). The Probe-error branch is not
	// reproducible with simple garbage, so we only assert it does not panic.
	assert.NotPanics(t, func() {
		_, _ = getM4ADuration(bytes.NewReader([]byte("not mp4 box data here padding")))
	})
}

func TestGetOGGDuration_Invalid(t *testing.T) {
	_, err := getOGGDuration(bytes.NewReader([]byte("not an ogg vorbis stream")))
	require.Error(t, err)
}

func TestGetAIFFDuration_Invalid(t *testing.T) {
	_, err := getAIFFDuration(bytes.NewReader([]byte("not aiff data here padding xx")))
	require.Error(t, err)
}

func TestGetAACDuration_Invalid(t *testing.T) {
	_, err := getAACDuration(bytes.NewReader([]byte("no adts frames present here")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid aac frames")
}

func TestGetWebMDuration(t *testing.T) {
	// Valid EBML magic -> "requires full EBML parser" error branch.
	ebml := append([]byte{0x1A, 0x45, 0xDF, 0xA3}, make([]byte, 60)...)
	_, err := getWebMDuration(bytes.NewReader(ebml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EBML")

	// Non-EBML content -> generic parse failure.
	_, err = getWebMDuration(bytes.NewReader([]byte("plain non-ebml content padding")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse webm")
}

func TestGetMP3Duration_GarbageTerminates(t *testing.T) {
	// The mp3 decoder scans for frame sync; on non-frame bytes it reaches EOF
	// and returns without error. Assert it terminates and yields a duration.
	d, err := getMP3Duration(bytes.NewReader([]byte("garbage-not-mp3-frames")))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, d, 0.0)
}

func TestGetOpusDuration_GarbageTerminates(t *testing.T) {
	// No "OggS" capture pattern present -> scan advances to EOF, duration 0.
	d, err := getOpusDuration(bytes.NewReader(make([]byte, 128)))
	require.NoError(t, err)
	assert.Equal(t, 0.0, d)
}

func TestGetAudioDuration_RoutesErrors(t *testing.T) {
	// Each routed codec on invalid data surfaces an error through the dispatcher.
	for _, ext := range []string{".flac", ".aiff", ".aif", ".aac", ".webm"} {
		_, err := GetAudioDuration(context.Background(), bytes.NewReader([]byte("invalid-bytes-padding-xx")), ext)
		assert.Error(t, err, "ext %s should error on invalid data", ext)
	}
}
