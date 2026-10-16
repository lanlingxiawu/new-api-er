package model

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 导出格式。
const (
	LogExportFormatCSVGz = "csv_gz"
	LogExportFormatXlsx  = "xlsx"
)

const (
	logExportFilePrefix = "log-export-"
	// logExportWriteBufSize 写缓冲，减少 syscall 次数。
	logExportWriteBufSize = 4 * 1024 * 1024
)

// ValidLogExportFormat 校验格式取值。
func ValidLogExportFormat(format string) bool {
	return format == LogExportFormatCSVGz || format == LogExportFormatXlsx
}

// LogExportPartFilePath 返回分片文件路径。
func LogExportPartFilePath(jobID string, part int, format string) string {
	ext := ".csv.gz"
	if format == LogExportFormatXlsx {
		ext = ".xlsx"
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s%s-part-%04d%s", logExportFilePrefix, jobID, part, ext))
}

// logExportPartWriter 是分片写入器。实现必须保证 Close 后文件可被完整读取。
type logExportPartWriter interface {
	WriteRow(record []string) error
	Rows() int64
	// Close 刷盘并关闭，返回文件字节数。
	Close() (int64, error)
}

// LogExportWriterOptions 写入选项。
type LogExportWriterOptions struct {
	// CSVBOM adds a UTF-8 BOM inside the compressed CSV for spreadsheet compatibility.
	CSVBOM bool
	// GzipLevel gzip 压缩等级，1 最省 CPU。
	GzipLevel int
	// Header 表头文案；为空则不写表头行。
	Header []string
}

// newLogExportPartWriter 按格式创建分片写入器，并写入表头。
func newLogExportPartWriter(path, format string, opts LogExportWriterOptions) (logExportPartWriter, error) {
	if format != LogExportFormatCSVGz {
		return nil, fmt.Errorf("unsupported export generation format: %s", format)
	}
	return newCSVGzPartWriter(path, opts)
}

// ── csv.gz ───────────────────────────────────────────────────────

type csvGzPartWriter struct {
	f    *os.File
	bw   *bufio.Writer
	gz   *gzip.Writer
	cw   *csv.Writer
	rows int64
}

func newCSVGzPartWriter(path string, opts LogExportWriterOptions) (logExportPartWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create export file: %w", err)
	}
	bw := bufio.NewWriterSize(f, logExportWriteBufSize)
	level := opts.GzipLevel
	if level < gzip.BestSpeed || level > gzip.BestCompression {
		level = gzip.BestSpeed
	}
	gz, err := gzip.NewWriterLevel(bw, level)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	w := &csvGzPartWriter{f: f, bw: bw, gz: gz, cw: csv.NewWriter(gz)}
	if opts.CSVBOM {
		// BOM 必须落在解压后的内容里，因此写进 gzip 流而不是外层文件。
		if _, err := gz.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
			_ = w.abort(path)
			return nil, err
		}
	}
	if len(opts.Header) > 0 {
		if err := w.cw.Write(opts.Header); err != nil {
			_ = w.abort(path)
			return nil, err
		}
	}
	return w, nil
}

func (w *csvGzPartWriter) abort(path string) error {
	_ = w.f.Close()
	return os.Remove(path)
}

func (w *csvGzPartWriter) WriteRow(record []string) error {
	for i, value := range record {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || !strings.ContainsRune("=+-@", rune(trimmed[0])) {
			continue
		}
		if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
			continue
		}
		record[i] = "'" + value
	}

	if err := w.cw.Write(record); err != nil {
		return err
	}
	w.rows++
	return nil
}

func (w *csvGzPartWriter) Rows() int64 { return w.rows }

func (w *csvGzPartWriter) Close() (int64, error) {
	w.cw.Flush()
	if err := w.cw.Error(); err != nil {
		_ = w.f.Close()
		return 0, err
	}
	if err := w.gz.Close(); err != nil {
		_ = w.f.Close()
		return 0, err
	}
	if err := w.bw.Flush(); err != nil {
		_ = w.f.Close()
		return 0, err
	}
	size := int64(0)
	if info, err := w.f.Stat(); err == nil {
		size = info.Size()
	}
	return size, w.f.Close()
}

func LogExportSummaryPartFilePath(jobID string, part int, format string) string {
	ext := ".csv.gz"
	if format == LogExportFormatXlsx {
		ext = ".xlsx"
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s%s-summary-%04d%s", logExportFilePrefix, jobID, part, ext))
}
