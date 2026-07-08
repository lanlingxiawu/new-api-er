package main

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAI 语音：
//   - TTS：POST {base}/v1/audio/speech → 二进制音频（网关按输入文本计费、字节透传）。
//   - STT：POST {base}/v1/audio/transcriptions | /translations → {"text":"..."}。
// 生成耗时用 -latency-* 模拟。

// audioBlob 预生成的伪音频字节；内容无需真实可播放，speech 是字节透传。
var audioBlob []byte

func initAudioBlob() {
	const n = 8 << 10 // 8KB
	audioBlob = make([]byte, n)
	copy(audioBlob, []byte("ID3")) // 假 ID3 头，避免全零
	for i := 3; i < n; i++ {
		audioBlob[i] = byte(i * 31)
	}
}

// audioSpeechHandler 处理 TTS：返回二进制音频。
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
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(audioBlob)))
	_, _ = w.Write(audioBlob)
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
