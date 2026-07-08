package main

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAI 语音：
//   - TTS：POST {base}/v1/audio/speech → 二进制音频（真实可播放 WAV，见 media.go）。
//   - STT：POST {base}/v1/audio/transcriptions | /translations → {"text":"..."}。
// 生成耗时用 -latency-* 模拟。

// audioSpeechHandler 处理 TTS：返回真实可播放的音频字节（默认生成的 WAV，或 -audio-file）。
func audioSpeechHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if code, inject := maybeErrorCode(); inject {
		statAudio.errs.Add(1)
		writeOpenAIError(w, code)
		return
	}
	statAudio.served.Add(1)
	time.Sleep(randDur(*totalMin, *totalMax))
	w.Header().Set("Content-Type", speechAsset.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(speechAsset.bytes)))
	_, _ = w.Write(speechAsset.bytes)
}

// audioTranscriptionHandler 处理 STT（transcriptions/translations）：返回 {"text":...}。
func audioTranscriptionHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(w, r); !ok {
		return
	}
	if code, inject := maybeErrorCode(); inject {
		statAudio.errs.Add(1)
		writeOpenAIError(w, code)
		return
	}
	statAudio.served.Add(1)
	time.Sleep(randDur(*totalMin, *totalMax))

	n := completionWords()
	buf := bufPool.Get().(*strings.Builder)
	buf.Reset()
	buf.WriteString(`{"text":"`)
	for i := 0; i < n; i++ {
		buf.Write(randWord())
	}
	buf.WriteString(`"}`)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = io.WriteString(w, buf.String())
	bufPool.Put(buf)
}
