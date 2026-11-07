package model

import (
	"errors"
	"math"
	"regexp"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

var (
	ErrLogExportInvalidGroupRatio = errors.New("invalid export group ratio")
	ErrLogExportReprice           = errors.New("cannot calculate export charge")
	logExportGroupRatioPattern    = regexp.MustCompile(`^[0-9]{1,5}(\.[0-9]{1,6})?$`)
)

// logExportRepricer belongs to one bounded export worker. It performs no I/O
// and keeps only the most recently encountered historical ratio.
type logExportRepricer struct {
	ratio        decimal.Decimal
	ratioText    string
	quotaPerUnit float64
	lastRaw      string
	lastRatio    decimal.Decimal
}

func parseLogExportGroupRatio(value string) (decimal.Decimal, error) {
	if !logExportGroupRatioPattern.MatchString(value) {
		return decimal.Zero, ErrLogExportInvalidGroupRatio
	}
	ratio, err := decimal.NewFromString(value)
	if err != nil || ratio.IsNegative() || ratio.GreaterThan(decimal.NewFromInt(10000)) {
		return decimal.Zero, ErrLogExportInvalidGroupRatio
	}
	return ratio, nil
}

func ValidateLogExportGroupRatioOverride(value string) error {
	_, err := parseLogExportGroupRatio(value)
	return err
}

func newLogExportRepricer(job *LogExportJob) (*logExportRepricer, error) {
	if job.GroupRatioOverride == nil {
		return nil, nil
	}
	if job.CreatorRole < common.RoleAdminUser || job.SelfExport || job.EmployeeScope != nil {
		return nil, ErrEmployeeExportAccess
	}
	ratio, err := parseLogExportGroupRatio(*job.GroupRatioOverride)
	if err != nil {
		return nil, err
	}
	unit := job.RepriceQuotaPerUnit
	if unit == 0 {
		unit = common.QuotaPerUnit
	}
	if unit <= 0 || math.IsInf(unit, 0) || math.IsNaN(unit) {
		return nil, ErrLogExportReprice
	}
	return &logExportRepricer{ratio: ratio, ratioText: ratio.String(), quotaPerUnit: unit}, nil
}

func (r *logExportRepricer) selectFields(fields []string) []string {
	if r == nil {
		return fields
	}
	for _, field := range []string{"type", "quota", "other"} {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	return fields
}

// prepareReprice runs after the original row filters and before both rendering
// and aggregation. It never edits the scanned log or its parsed other map.
func (ctx *rowCtx) prepareReprice(l *Log) error {
	ctx.repriceOwner, ctx.repriced, ctx.repricedSpecial = l, false, false
	r := ctx.repricer
	if r == nil || l.Type != LogTypeConsume || l.Other == "" {
		return nil
	}
	if !gjson.Valid(l.Other) {
		return ErrLogExportReprice
	}
	old := gjson.Get(l.Other, "group_ratio")
	if old.Type == gjson.Null {
		return nil
	}
	if old.Type != gjson.Number || len(old.Raw) > 64 {
		return ErrLogExportReprice
	}
	if old.Raw != r.lastRaw {
		parsed, err := decimal.NewFromString(old.Raw)
		if err != nil || parsed.Exponent() < -100 || parsed.Exponent() > 100 || parsed.IsNegative() {
			return ErrLogExportReprice
		}
		r.lastRaw, r.lastRatio = old.Raw, parsed
	}
	if r.lastRatio.IsZero() {
		return nil
	}
	if l.Quota < 0 {
		return ErrLogExportReprice
	}
	quota := l.Quota
	if !r.ratio.Equal(r.lastRatio) {
		if r.ratio.IsZero() || quota == 0 {
			quota = 0
		} else {
			// Divide and round only once: caching a rounded N/G would change
			// half-quota boundaries for small charges.
			amount := decimal.NewFromInt(int64(quota)).Mul(r.ratio).DivRound(r.lastRatio, 0)
			var clamp *common.QuotaClamp
			quota, clamp = common.QuotaFromDecimalChecked(amount)
			if clamp != nil {
				return ErrLogExportReprice
			}
		}
	}
	ctx.repriced, ctx.repricedQuota = true, quota
	special := gjson.Get(l.Other, "user_group_ratio")
	ctx.repricedSpecial = special.Type == gjson.Number && special.Float() >= 0
	return nil
}

func (ctx *rowCtx) exportQuota(l *Log) int {
	if ctx.repriced && ctx.repriceOwner == l {
		return ctx.repricedQuota
	}
	return l.Quota
}
