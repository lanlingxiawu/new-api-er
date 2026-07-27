package model

import (
	"compress/gzip"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// readCSVGz 解压并解析一个 csv.gz 分片，返回全部行（含表头）与是否带 BOM。
func readCSVGz(t *testing.T, path string) (rows [][]string, hasBOM bool) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	gz, err := gzip.NewReader(f)
	require.NoError(t, err, "part must be a valid gzip stream")
	defer gz.Close()

	data, err := readAll(gz)
	require.NoError(t, err)

	if strings.HasPrefix(string(data), "\xEF\xBB\xBF") {
		hasBOM = true
		data = data[3:]
	}
	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.FieldsPerRecord = -1
	rows, err = reader.ReadAll()
	require.NoError(t, err)
	return rows, hasBOM
}

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var out []byte
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}

func TestCSVGzPartWriter_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "part.csv.gz")
	w, err := newLogExportPartWriter(path, LogExportFormatCSVGz, LogExportWriterOptions{
		CSVBOM:    true,
		GzipLevel: 1,
		Header:    []string{"时间", "用户"},
	})
	require.NoError(t, err)
	require.NoError(t, w.WriteRow([]string{"2026-07-27 10:00:00", "alice"}))
	require.NoError(t, w.WriteRow([]string{"2026-07-27 10:00:01", "bob"}))
	assert.Equal(t, int64(2), w.Rows())

	size, err := w.Close()
	require.NoError(t, err)
	assert.Greater(t, size, int64(0))

	rows, hasBOM := readCSVGz(t, path)
	assert.True(t, hasBOM, "BOM must be inside the gzip stream so Excel reads UTF-8 correctly")
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"时间", "用户"}, rows[0])
	assert.Equal(t, []string{"2026-07-27 10:00:01", "bob"}, rows[2])
}

func TestCSVGzPartWriter_WithoutBOMAndHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "part.csv.gz")
	w, err := newLogExportPartWriter(path, LogExportFormatCSVGz, LogExportWriterOptions{GzipLevel: 1})
	require.NoError(t, err)
	require.NoError(t, w.WriteRow([]string{"a", "b"}))
	_, err = w.Close()
	require.NoError(t, err)

	rows, hasBOM := readCSVGz(t, path)
	assert.False(t, hasBOM)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"a", "b"}, rows[0])
}

// 字段里的逗号/引号/换行/中文必须被正确转义，否则文件在 Excel 里会错列。
func TestCSVGzPartWriter_EscapesSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "part.csv.gz")
	w, err := newLogExportPartWriter(path, LogExportFormatCSVGz, LogExportWriterOptions{GzipLevel: 1})
	require.NoError(t, err)
	tricky := []string{`a,b`, `say "hi"`, "line1\nline2", "中文字段"}
	require.NoError(t, w.WriteRow(tricky))
	_, err = w.Close()
	require.NoError(t, err)

	rows, _ := readCSVGz(t, path)
	require.Len(t, rows, 1)
	assert.Equal(t, tricky, rows[0])
}

func TestCSVGzPartWriter_GzipLevelsAllReadable(t *testing.T) {
	for _, level := range []int{1, 6, 9, 0 /* 非法值应回落到 BestSpeed */} {
		level := level
		t.Run("level"+strconv.Itoa(level), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "part.csv.gz")
			w, err := newLogExportPartWriter(path, LogExportFormatCSVGz, LogExportWriterOptions{GzipLevel: level})
			require.NoError(t, err)
			require.NoError(t, w.WriteRow([]string{"x"}))
			_, err = w.Close()
			require.NoError(t, err)

			rows, _ := readCSVGz(t, path)
			require.Len(t, rows, 1)
			assert.Equal(t, []string{"x"}, rows[0])
		})
	}
}

func TestXlsxPartWriter_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "part.xlsx")
	w, err := newLogExportPartWriter(path, LogExportFormatXlsx, LogExportWriterOptions{
		Header: []string{"Time", "User"},
	})
	require.NoError(t, err)
	require.NoError(t, w.WriteRow([]string{"2026-07-27 10:00:00", "alice"}))
	assert.Equal(t, int64(1), w.Rows(), "header must not count as a data row")

	size, err := w.Close()
	require.NoError(t, err)
	assert.Greater(t, size, int64(0))

	f, err := excelize.OpenFile(path)
	require.NoError(t, err)
	defer f.Close()
	rows, err := f.GetRows("Sheet1")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"Time", "User"}, rows[0])
	assert.Equal(t, []string{"2026-07-27 10:00:00", "alice"}, rows[1])
}

func TestLogExportPartFilePath_ExtensionByFormat(t *testing.T) {
	csvPath := LogExportPartFilePath("job1", 1, LogExportFormatCSVGz)
	assert.True(t, strings.HasSuffix(csvPath, "-part-0001.csv.gz"), csvPath)
	xlsxPath := LogExportPartFilePath("job1", 12, LogExportFormatXlsx)
	assert.True(t, strings.HasSuffix(xlsxPath, "-part-0012.xlsx"), xlsxPath)
	assert.Contains(t, filepath.Base(csvPath), logExportFilePrefix)
}

func TestValidLogExportFormat(t *testing.T) {
	assert.True(t, ValidLogExportFormat(LogExportFormatCSVGz))
	assert.True(t, ValidLogExportFormat(LogExportFormatXlsx))
	assert.False(t, ValidLogExportFormat("parquet"))
	assert.False(t, ValidLogExportFormat(""))
}

func TestJobIDFromExportFileName(t *testing.T) {
	assert.Equal(t, "abc-def", jobIDFromExportFileName("log-export-abc-def-part-0001.csv.gz"))
	assert.Equal(t, "abc", jobIDFromExportFileName("log-export-abc-part-0002.xlsx"))
	assert.Equal(t, "", jobIDFromExportFileName("unrelated-file.csv.gz"))
	assert.Equal(t, "", jobIDFromExportFileName("log-export-nopart.csv.gz"))
}

func TestLogExportProgress(t *testing.T) {
	// 刚开始扫描（position 接近 end）→ 下限 1。
	assert.Equal(t, 1, logExportProgress(0, 1000, 1000))
	// 扫过一半 → 约 49。
	assert.Equal(t, 49, logExportProgress(0, 1000, 500))
	// 扫到起点 → 99（100 留给完成态）。
	assert.Equal(t, 99, logExportProgress(0, 1000, 0))
	// 退化区间不应除零。
	assert.Equal(t, 99, logExportProgress(500, 500, 500))
}
