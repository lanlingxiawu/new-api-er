package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// feedAll 按 chunks 顺序喂入剥离器，返回应计费文本与确认剥离的媒体数量。
func feedAll(s *DataURLScrubber, chunks ...string) (string, int) {
	var clean strings.Builder
	media := 0
	for _, chunk := range chunks {
		text, n := s.Feed(chunk)
		clean.WriteString(text)
		media += n
	}
	return clean.String(), media
}

// splitPoints 返回用于增量等价断言的切分位置：短输入全覆盖，长输入取关键边界。
func splitPoints(text string) []int {
	if len(text) <= 80 {
		points := make([]int, 0, len(text)+1)
		for i := 0; i <= len(text); i++ {
			points = append(points, i)
		}
		return points
	}
	points := []int{0, 1, len(text)}
	for _, marker := range []string{"dat", "data:", ";base6", ";base64,"} {
		if i := strings.Index(text, marker); i >= 0 {
			points = append(points, i, i+len(marker))
		}
	}
	points = append(points, len(text)/2, len(text)-1)
	return points
}

func TestDataURLScrubber(t *testing.T) {
	b255 := strings.Repeat("A", 255)
	b256 := strings.Repeat("A", 256)
	b257 := strings.Repeat("A", 257)
	b300 := strings.Repeat("QUJD", 75) // 300 字符合法 base64

	cases := []struct {
		name  string
		in    string
		clean string
		media int
	}{
		{"纯文本无 data", "hello world, nothing to strip", "hello world, nothing to strip", 0},
		{"data 但无 base64 标记", "data:text/plain,hello", "data:text/plain,hello", 0},
		{"以 data 开头的普通讨论", "the data: prefix is just text here", "the data: prefix is just text here", 0},
		{"markdown 图片", "before ![image](data:image/png;base64," + b300 + ") after", "before ![image]() after", 1},
		{"非图片媒体", "x [media](data:audio/mpeg;base64," + b300 + ") y", "x [media]() y", 1},
		{"裸 data URL 未闭合（流中断）", "x data:image/png;base64," + b300, "x ", 1},
		{"阈值下界 255 按文本计费", "a data:image/png;base64," + b255 + ")", "a data:image/png;base64," + b255 + ")", 0},
		{"阈值 256 判定媒体", "a data:image/png;base64," + b256 + ")", "a )", 1},
		{"阈值上界 257 判定媒体", "a data:image/png;base64," + b257 + ")", "a )", 1},
		{"一帧内多个 data URL", "p ![a](data:image/png;base64," + b300 + ") q ![b](data:image/jpeg;base64," + b300 + ") r", "p ![a]() q ![b]() r", 2},
		{"大小写不敏感", "a DATA:image/png;BASE64," + b300 + ")b", "a )b", 1},
		{"紧邻普通文本两侧", "abdata:image/png;base64," + b300 + " cx", "ab cx", 1},
		// 尾部 "d" 是 data: 的真前缀，留到下一批再判定，所以本批不计费（有界 ≤4 字节）。
		{"剥离后尾字符恰为 data 前缀", "abdata:image/png;base64," + b300 + " cd", "ab c", 1},
		{"头部超长不含 base64 标记", "data:" + strings.Repeat("x", 200) + "!tail", "data:" + strings.Repeat("x", 200) + "!tail", 0},
		{"文本以完整前缀结束（有界未计费尾）", "end with data:", "end with ", 0},
		{"文本以部分前缀结束（有界未计费尾）", "abc dat", "abc ", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s DataURLScrubber
			clean, media := feedAll(&s, tc.in)
			require.Equal(t, tc.clean, clean)
			require.Equal(t, tc.media, media)

			for _, at := range splitPoints(tc.in) {
				var chunked DataURLScrubber
				gotClean, gotMedia := feedAll(&chunked, tc.in[:at], tc.in[at:])
				require.Equal(t, tc.clean, gotClean, "切分位置 %d 的应计费文本必须与一次性输入一致", at)
				require.Equal(t, tc.media, gotMedia, "切分位置 %d 的媒体数量必须与一次性输入一致", at)
			}
		})
	}
}

// TestDataURLScrubberTerminators 覆盖每一个终止符，终止符本身必须仍然计费。
func TestDataURLScrubberTerminators(t *testing.T) {
	body := strings.Repeat("QUJD", 75)
	for _, terminator := range []string{")", " ", "\n", "\r", "\t", "\"", "'", "`", "<", "]"} {
		t.Run(fmt.Sprintf("%q", terminator), func(t *testing.T) {
			in := "a data:image/png;base64," + body + terminator + "b"
			var s DataURLScrubber
			clean, media := feedAll(&s, in)
			require.Equal(t, "a "+terminator+"b", clean)
			require.Equal(t, 1, media)
		})
	}
}

// TestDataURLScrubberSplitInsideBody 断言 base64 正文被逐字节切开时张数既不丢也不重复。
func TestDataURLScrubberSplitInsideBody(t *testing.T) {
	head := "x ![image](data:image/png;base64,"
	body := strings.Repeat("QUJD", 200)
	in := head + body + ")"
	for at := 0; at <= len(in); at += 7 {
		var s DataURLScrubber
		clean, media := feedAll(&s, in[:at], in[at:])
		require.Equal(t, "x ![image]()", clean, "切分位置 %d", at)
		require.Equal(t, 1, media, "切分位置 %d", at)
	}
}
