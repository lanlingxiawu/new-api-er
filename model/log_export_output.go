package model

import (
	"os"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// Shared streaming output for usage and audit details. Abort also handles panics
// and Close errors, including a current part not yet present in job.Parts.
type logExportOutput struct {
	job      *LogExportJob
	opts     LogExportWriterOptions
	writer   logExportPartWriter
	part     LogExportPart
	finished bool
}

func newLogExportOutput(job *LogExportJob, header []string) (*logExportOutput, error) {
	if err := CheckLogExportDiskSpace(); err != nil {
		return nil, err
	}
	if !job.Options.Header {
		header = nil
	}
	o := &logExportOutput{job: job, opts: LogExportWriterOptions{Header: header, CSVBOM: job.Options.CSVBOM}}
	if err := o.open(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *logExportOutput) open() error {
	i := len(o.job.Parts) + 1
	o.part = LogExportPart{Index: i, Path: LogExportPartFilePath(o.job.JobID, i, o.job.Format), StartTime: o.job.Filters.StartTimestamp, EndTime: o.job.Filters.StartTimestamp}
	o.opts.GzipLevel = operation_setting.GetLogExportSetting().GetGzipLevel()
	var err error
	o.writer, err = newLogExportPartWriter(o.part.Path, o.job.Format, o.opts)
	return err
}

func (o *logExportOutput) close() error {
	bytes, err := o.writer.Close()
	o.writer = nil
	if err != nil {
		return err
	}
	o.part.Bytes = bytes
	o.job.BytesOut += bytes
	o.job.Parts = append(o.job.Parts, o.part)
	o.part.Path = ""
	return nil
}

func (o *logExportOutput) Write(row []string, at int64) error {
	if o.part.Rows >= int64(operation_setting.GetLogExportSetting().GetRowsPerFile()) {
		if err := o.close(); err != nil {
			return err
		}
		if len(o.job.Parts) >= operation_setting.GetLogExportSetting().GetMaxParts() {
			return ErrLogExportTooManyParts
		}
		if err := CheckLogExportDiskSpace(); err != nil {
			return err
		}
		if err := o.open(); err != nil {
			return err
		}
	}
	if err := o.writer.WriteRow(row); err != nil {
		return err
	}
	if o.part.Rows == 0 {
		o.part.StartTime, o.part.EndTime = at, at
	}
	o.part.StartTime = min(o.part.StartTime, at)
	o.part.EndTime = max(o.part.EndTime, at)
	o.part.Rows++
	o.job.RowCount++
	return nil
}

func (o *logExportOutput) Finish() error {
	if err := o.close(); err != nil {
		return err
	}
	o.finished = true
	return nil
}

func (o *logExportOutput) Abort() {
	if o.finished {
		return
	}
	if o.writer != nil {
		_, _ = o.writer.Close()
		o.writer = nil
	}
	if o.part.Path != "" {
		_ = os.Remove(o.part.Path)
	}
	RemoveLogExportJobFiles(o.job)
}
