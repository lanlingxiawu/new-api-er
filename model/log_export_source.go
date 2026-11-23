package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const (
	LogExportDataUsage = "usage"
	LogExportDataAudit = "audit"
	LogExportDataQuota = "quota"
)

var (
	ErrLogExportDataSource    = errors.New("unsupported export data source")
	ErrLogExportAccessChanged = errors.New("export access changed")
	ErrLogExportRiskChanged   = errors.New("export risk revision changed")
)

type AuditExportFilter struct {
	AuditLogFilter
	IPRisk string `json:"ip_risk,omitempty"`
}

func (j *LogExportJob) EffectiveDataSource() string {
	if j.DataSource == "" {
		return LogExportDataUsage
	}
	return j.DataSource
}

type LogExportRow struct {
	Values []string
	At     int64
}

// Cursor and scanned count describe candidates, independently of rendered rows.
type LogExportBatch struct {
	Rows     []LogExportRow
	Logs     []*Log
	rawLogs  []*logExportRawLog
	Scanned  int
	Cursor   any
	Position int64
	Done     bool
}

type LogExportDataReader interface {
	Header() []string
	BatchSize(configured int) int
	Read(context.Context, int64, int64, any, int) (LogExportBatch, error)
	Validate(context.Context, bool) error
	// Reverse reports that windows move from the end of the range backward.
	// Audit does. Quota data and usage logs move forward.
	Reverse() bool
}

// Registered once during service initialization; only the audit source is extensible.
var auditExportReader func(context.Context, *LogExportJob) (LogExportDataReader, error)

func RegisterAuditExportReader(factory func(context.Context, *LogExportJob) (LogExportDataReader, error)) {
	if factory == nil || auditExportReader != nil {
		panic("audit export reader already registered or nil")
	}
	auditExportReader = factory
}

func writeAuditLogExport(ctx context.Context, job *LogExportJob) error {
	if !job.HasCurrentAuditProjection() {
		return ErrLogExportAccessChanged
	}
	if auditExportReader == nil || job.AuditFilters == nil || job.EmployeeScope != nil || job.NeedsSummary() || job.GroupRatioOverride != nil {
		return ErrLogExportDataSource
	}
	if job.SelfExport && (!job.IsSelfExportFor(job.UserID) || !job.AuditFilters.SelfView || job.AuditFilters.UserId != job.UserID || job.AuditFilters.IPRisk != "") {
		return ErrLogExportAccessChanged
	}
	reader, err := auditExportReader(ctx, job)
	if err != nil {
		return err
	}
	validationCtx, cancel := context.WithTimeout(ctx, logExportQueryTimeout(operation_setting.GetLogExportSetting().GetBatchQueryTimeoutSec()))
	err = reader.Validate(validationCtx, true)
	cancel()
	if err != nil {
		return err
	}
	output, err := newLogExportOutput(job, reader.Header())
	if err != nil {
		return err
	}
	defer output.Abort()
	scanner := newLogExportScanner(job, nil, false, nil)
	scanner.reader = reader
	err = scanner.runBatches(ctx, func(batch LogExportBatch) error {
		if err := CheckLogExportDiskSpace(); err != nil {
			return err
		}
		for _, row := range batch.Rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := output.Write(row.Values, row.At); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	validationCtx, cancel = context.WithTimeout(ctx, logExportQueryTimeout(operation_setting.GetLogExportSetting().GetBatchQueryTimeoutSec()))
	err = reader.Validate(validationCtx, true)
	cancel()
	if err != nil {
		return err
	}
	return output.Finish()
}
