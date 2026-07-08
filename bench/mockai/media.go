package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// media.go 让图片 / 语音 / 视频返回**真实可用**的媒体，并把媒体挂在 mock 自身的
// /media/ 路径下，使响应里的 URL 真实可下载（而非指向不存在的地址）。
//
// 标准库可编码 PNG / WAV / GIF（均真实可播放），但**无 MP4 编码器**：视频默认用
// 可播放的动图 GIF 占位。需要真实 MP4/MP3/JPEG 等精确格式时，用 -video-file /
// -audio-file / -image-file 指定真实素材文件（按扩展名推断 Content-Type）。
//
// 媒体在启动时生成一次，之后只做字节直写，不影响热路径。

type mediaAsset struct {
	bytes       []byte
	contentType string
	ext         string
}

var (
	imageAsset  mediaAsset
	speechAsset mediaAsset
	videoAsset  mediaAsset
	imageB64    string // 预编码的图片 base64，用于 response_format=b64_json
)

func initMedia() {
	imageAsset = loadOrGen(*imageFile, "image/png", "png", genPNG)
	speechAsset = loadOrGen(*audioFile, "audio/wav", "wav", genWAV)
	videoAsset = loadOrGen(*videoFile, "image/gif", "gif", genGIF)
	imageB64 = base64.StdEncoding.EncodeToString(imageAsset.bytes)
}

// loadOrGen：有 file 则读文件（按扩展名推断 Content-Type），否则用 gen 生成默认素材。
func loadOrGen(file, defCT, defExt string, gen func() []byte) mediaAsset {
	if file != "" {
		if b, err := os.ReadFile(file); err == nil && len(b) > 0 {
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(file)), ".")
			return mediaAsset{bytes: b, contentType: ctByExt(ext, defCT), ext: ext}
		}
		// 读失败回落默认，避免 mock 起不来
	}
	return mediaAsset{bytes: gen(), contentType: defCT, ext: defExt}
}

func ctByExt(ext, def string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "opus":
		return "audio/opus"
	case "flac":
		return "audio/flac"
	case "aac":
		return "audio/aac"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mov":
		return "video/quicktime"
	}
	return def
}

// genPNG 生成 512x512 渐变 PNG（真实可显示）。
func genPNG() []byte {
	const W, H = 512, 512
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x / 2), G: uint8(y / 2), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// genWAV 生成 1 秒 16kHz 16-bit 单声道 440Hz 正弦音（真实可播放 WAV）。
func genWAV() []byte {
	const sampleRate = 16000
	const seconds = 1
	const freq = 440.0
	n := sampleRate * seconds

	pcm := new(bytes.Buffer)
	for i := 0; i < n; i++ {
		v := int16(0.3 * 32767 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		_ = binary.Write(pcm, binary.LittleEndian, v)
	}
	data := pcm.Bytes()

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+len(data)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16)) // PCM fmt chunk size
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*2)) // byte rate
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))            // block align
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))           // bits/sample
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}

// genGIF 生成一段动图 GIF（真实可播放，作为视频占位）。
func genGIF() []byte {
	const W, H, frames = 128, 128, 8
	pal := color.Palette{
		color.Black,
		color.RGBA{R: 255, A: 255},
		color.RGBA{G: 255, A: 255},
		color.RGBA{B: 255, A: 255},
		color.White,
	}
	g := &gif.GIF{}
	for f := 0; f < frames; f++ {
		img := image.NewPaletted(image.Rect(0, 0, W, H), pal)
		ci := uint8(1 + f%(len(pal)-1))
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				if (x+y+f*8)%32 < 16 {
					img.SetColorIndex(x, y, ci)
				}
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10) // 100ms/帧
	}
	var buf bytes.Buffer
	_ = gif.EncodeAll(&buf, g)
	return buf.Bytes()
}

// mediaHandler 按 /media/{image,audio,video}/... 路径返回对应真实媒体字节。
func mediaHandler(w http.ResponseWriter, p string) {
	var a mediaAsset
	switch {
	case strings.Contains(p, "/media/image"):
		a = imageAsset
	case strings.Contains(p, "/media/audio"):
		a = speechAsset
	case strings.Contains(p, "/media/video"):
		a = videoAsset
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(a.bytes)))
	_, _ = w.Write(a.bytes)
}

// mediaURL 构造指向 mock 自身、真实可下载的媒体 URL（host 取自请求，保证客户端可达）。
func mediaURL(host, kind, id, ext string) string {
	return "http://" + host + "/media/" + kind + "/" + id + "." + ext
}
