package xai

import (
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const (
	defaultVideoDurationSeconds = 5
	maxInlineImageSizeBytes     = 20 * 1024 * 1024
)

func resolveDurationSeconds(metadata map[string]any, stdDuration int, stdSeconds string) int {
	if metadata != nil {
		for _, key := range []string{"duration", "durationSeconds"} {
			if v, ok := metadata[key]; ok {
				if seconds := positiveInt(v); seconds > 0 {
					return min(seconds, relaycommon.MaxTaskDurationSeconds)
				}
			}
		}
	}
	if stdDuration > 0 {
		return min(stdDuration, relaycommon.MaxTaskDurationSeconds)
	}
	if seconds, err := strconv.Atoi(stdSeconds); err == nil && seconds > 0 {
		return min(seconds, relaycommon.MaxTaskDurationSeconds)
	}
	return defaultVideoDurationSeconds
}

func positiveInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		if !math.IsNaN(n) && n > 0 {
			return int(n)
		}
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

func resolveAspectRatio(metadata map[string]any, stdSize string) string {
	if metadata != nil {
		for _, key := range []string{"aspect_ratio", "aspectRatio"} {
			if v, ok := metadata[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	if stdSize != "" {
		return sizeToAspectRatio(stdSize)
	}
	return "16:9"
}

func resolveResolution(metadata map[string]any, stdSize string) string {
	if metadata != nil {
		if v, ok := metadata["resolution"].(string); ok && strings.TrimSpace(v) != "" {
			return normalizeResolution(v)
		}
	}
	if stdSize != "" {
		return sizeToResolution(stdSize)
	}
	return "720p"
}

func normalizeResolution(resolution string) string {
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	switch resolution {
	case "480", "480p":
		return "480p"
	case "720", "720p":
		return "720p"
	case "1080", "1080p":
		return "1080p"
	default:
		return resolution
	}
}

func sizeToAspectRatio(size string) string {
	w, h := parseSize(size)
	if w <= 0 || h <= 0 {
		return "16:9"
	}
	if w == h {
		return "1:1"
	}
	if w > h {
		return "16:9"
	}
	return "9:16"
}

func sizeToResolution(size string) string {
	w, h := parseSize(size)
	if w <= 0 || h <= 0 {
		return "720p"
	}
	shortSide := min(w, h)
	if shortSide >= 1080 {
		return "1080p"
	}
	if shortSide >= 720 {
		return "720p"
	}
	return "480p"
}

func parseSize(size string) (int, int) {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(size)), "x", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
	return w, h
}

func xaiVideoUnits(modelName, resolution string, seconds int, imageCount int) float64 {
	if seconds <= 0 {
		seconds = defaultVideoDurationSeconds
	}
	if imageCount < 0 {
		imageCount = 0
	}

	resolutionRatio, imageUnit := xaiPricingUnits(modelName, supportedResolution(modelName, resolution))
	return float64(seconds)*resolutionRatio + float64(imageCount)*imageUnit
}

func supportedResolution(modelName, resolution string) string {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	resolution = normalizeResolution(resolution)
	if strings.Contains(modelName, "grok-imagine-video-1.5") {
		return resolution
	}
	if strings.Contains(modelName, "grok-imagine-video") && resolution == "1080p" {
		return "720p"
	}
	return resolution
}

func xaiPricingUnits(modelName, resolution string) (resolutionRatio float64, imageUnit float64) {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	resolution = normalizeResolution(resolution)

	if strings.Contains(modelName, "grok-imagine-video-1.5") {
		switch resolution {
		case "1080p":
			resolutionRatio = 3.125
		case "720p":
			resolutionRatio = 1.75
		default:
			resolutionRatio = 1
		}
		return resolutionRatio, 0.125
	}
	if !strings.Contains(modelName, "grok-imagine-video") {
		return 1, 0
	}

	switch resolution {
	case "720p":
		resolutionRatio = 1.4
	default:
		resolutionRatio = 1
	}
	return resolutionRatio, 0.04
}

func firstRequestImage(c *gin.Context, req relaycommon.TaskSubmitReq, meta *requestMetadata, info *relaycommon.RelayInfo) *videoInput {
	if img := extractMultipartImage(c, info); img != nil {
		return img
	}
	if meta != nil && meta.Image != nil && !meta.Image.empty() {
		return meta.Image
	}
	if input := parseVideoInput(req.Image); input != nil {
		return input
	}
	if len(req.Images) == 1 {
		return parseVideoInput(req.Images[0])
	}
	return nil
}

func requestReferenceImages(req relaycommon.TaskSubmitReq, meta *requestMetadata) []videoInput {
	var refs []videoInput
	if meta != nil {
		for _, ref := range meta.ReferenceImages {
			if !ref.empty() {
				refs = append(refs, ref)
			}
		}
	}
	if len(refs) > 0 {
		return refs
	}
	if len(req.Images) <= 1 {
		return nil
	}
	for _, raw := range req.Images {
		if input := parseVideoInput(raw); input != nil {
			refs = append(refs, *input)
		}
	}
	return refs
}

func imageInputCount(req relaycommon.TaskSubmitReq, meta *requestMetadata) int {
	seen := make(map[string]struct{})
	add := func(input *videoInput) {
		if input == nil || input.empty() {
			return
		}
		key := input.URL
		if key == "" {
			key = "file:" + input.FileID
		}
		seen[key] = struct{}{}
	}
	add(parseVideoInput(req.Image))
	for _, raw := range req.Images {
		add(parseVideoInput(raw))
	}
	if meta != nil {
		add(meta.Image)
		for i := range meta.ReferenceImages {
			add(&meta.ReferenceImages[i])
		}
	}
	return len(seen)
}

func parseVideoInput(raw string) *videoInput {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "file_") {
		return &videoInput{FileID: raw}
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "data:") {
		return &videoInput{URL: raw}
	}
	if dataURL := rawBase64ImageToDataURL(raw); dataURL != "" {
		return &videoInput{URL: dataURL}
	}
	return nil
}

func rawBase64ImageToDataURL(raw string) string {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil || len(decoded) == 0 || len(decoded) > maxInlineImageSizeBytes {
		return ""
	}
	mimeType := http.DetectContentType(decoded)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(decoded))
}

func extractMultipartImage(c *gin.Context, info *relaycommon.RelayInfo) *videoInput {
	if c == nil || c.Request == nil {
		return nil
	}
	multipartForm, err := c.MultipartForm()
	if err != nil || multipartForm == nil {
		return nil
	}
	for _, field := range []string{"input_reference", "image"} {
		files := multipartForm.File[field]
		if len(files) == 0 {
			continue
		}
		fh := files[0]
		if fh.Size > maxInlineImageSizeBytes {
			return nil
		}
		file, err := fh.Open()
		if err != nil {
			return nil
		}
		data, readErr := io.ReadAll(file)
		_ = file.Close()
		if readErr != nil || len(data) == 0 {
			return nil
		}
		mimeType := fh.Header.Get("Content-Type")
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = http.DetectContentType(data)
		}
		if info != nil {
			info.Action = constant.TaskActionGenerate
		}
		return &videoInput{URL: fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))}
	}
	return nil
}

func (v videoInput) empty() bool {
	return strings.TrimSpace(v.URL) == "" && strings.TrimSpace(v.FileID) == ""
}
