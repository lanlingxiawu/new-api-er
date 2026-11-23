package auditexport

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
)

func init() { model.RegisterAuditExportReader(NewReader) }

type Reader struct {
	job        *model.LogExportJob
	main, logs *gorm.DB
	loc        *time.Location
	validated  time.Time
	revision   *int64
	coverage   auditsecurity.RiskCoverage
}

func NewReader(ctx context.Context, job *model.LogExportJob) (model.LogExportDataReader, error) {
	main, err := model.AuditExportDB(false)
	if err != nil {
		return nil, err
	}
	logs, err := model.AuditExportDB(true)
	if err != nil {
		return nil, err
	}
	loc := time.Local
	if job.Options.Timezone != "" {
		loc, err = time.LoadLocation(job.Options.Timezone)
		if err != nil {
			return nil, err
		}
	}
	return &Reader{job: job, main: main, logs: logs, loc: loc}, nil
}

// CurrentAccess reads the actual account and current rules through the export pool.
func CurrentAccess(ctx context.Context, userID int, scope string) (model.LogExportAccess, error) {
	db, err := model.AuditExportDB(false)
	if err != nil {
		return model.LogExportAccess{}, err
	}
	return currentAccess(ctx, db, userID, scope)
}

func currentAccess(ctx context.Context, db *gorm.DB, userID int, scope string) (model.LogExportAccess, error) {
	var user model.User
	err := db.WithContext(ctx).Select("id", "role", "status").Where("id = ?", userID).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && (user.Status != common.UserStatusEnabled || user.Role < common.RoleCommonUser) {
		return model.LogExportAccess{}, model.ErrEmployeeExportAccess
	}
	if err != nil {
		return model.LogExportAccess{}, err
	}
	a := model.LogExportAccess{UserID: userID, Role: user.Role, Scope: scope}
	if scope == "audit_self" {
		return a, nil
	}
	if scope != "audit_admin" || user.Role < common.RoleAdminUser {
		return a, model.ErrEmployeeExportAccess
	}
	p, err := authz.ReadCurrentPermissionsFromDB(ctx, db, userID, user.Role, []authz.Permission{authz.AuditRead, authz.AuditExport, authz.AuditSecurityRead, authz.AuditSecurityExport})
	if err != nil {
		return a, err
	}
	a.AuditRead, a.AuditSecurityExport = p[authz.AuditRead], p[authz.AuditSecurityRead] && p[authz.AuditSecurityExport]
	a.AuditExport = p[authz.AuditExport]
	if !a.AuditRead || !a.AuditExport {
		return a, model.ErrEmployeeExportAccess
	}
	return a, nil
}

func (r *Reader) Validate(ctx context.Context, force bool) error {
	if !r.job.HasCurrentAuditProjection() {
		return model.ErrLogExportAccessChanged
	}
	if force || time.Since(r.validated) >= time.Second {
		scope := "audit_admin"
		if r.job.SelfExport {
			scope = "audit_self"
		}
		a, err := currentAccess(ctx, r.main, r.job.UserID, scope)
		if errors.Is(err, model.ErrEmployeeExportAccess) {
			return model.ErrLogExportAccessChanged
		}
		if err != nil {
			return err
		}
		if !a.CanReadData(r.job) || !model.CanFilterAuditActor(r.job.AuditFilters.ActorType, a.Role) {
			return model.ErrLogExportAccessChanged
		}
		r.validated = time.Now()
	}
	if r.job.AuditFilters.IPRisk != "" {
		var state model.SecurityRiskState
		if err := r.main.WithContext(ctx).Where("name = ?", "risk").Take(&state).Error; err != nil {
			return err
		}
		if r.revision != nil && *r.revision != state.Revision {
			return model.ErrLogExportRiskChanged
		}
		if r.revision == nil {
			r.revision = &state.Revision
			r.coverage = auditsecurity.CurrentRiskCoverage()
		}
	}
	return ctx.Err()
}

func (r *Reader) Header() []string {
	keys := []string{i18n.MsgAuditExportColumnEventID, i18n.MsgAuditExportColumnTime, i18n.MsgAuditExportColumnUserID, i18n.MsgAuditExportColumnUsername, i18n.MsgAuditExportColumnActorRole, i18n.MsgAuditExportColumnActorIsEmployee, i18n.MsgAuditExportColumnCategory, i18n.MsgAuditExportColumnAction, i18n.MsgAuditExportColumnAuthMethod, i18n.MsgAuditExportColumnIP, i18n.MsgAuditExportColumnUserAgent, i18n.MsgAuditExportColumnMethod, i18n.MsgAuditExportColumnRoute, i18n.MsgAuditExportColumnStatus, i18n.MsgAuditExportColumnSuccess, i18n.MsgAuditExportColumnRequestID, i18n.MsgAuditExportColumnTokenRef, i18n.MsgAuditExportColumnContent, i18n.MsgAuditExportColumnOther}
	if r.job.SelfExport {
		keys = []string{i18n.MsgAuditExportColumnEventID, i18n.MsgAuditExportColumnTime, i18n.MsgAuditExportColumnCategory, i18n.MsgAuditExportColumnAction, i18n.MsgAuditExportColumnSuccess, i18n.MsgAuditExportColumnIP, i18n.MsgAuditExportColumnClient, i18n.MsgAuditExportColumnAuthMethod, i18n.MsgAuditExportColumnLoginMethod, i18n.MsgAuditExportColumnTarget, i18n.MsgAuditExportColumnChanges, i18n.MsgAuditExportColumnChangeCount, i18n.MsgAuditExportColumnChangeResult, i18n.MsgAuditExportColumnRequestID}
	}
	if r.job.AuditFilters.IPRisk != "" {
		keys = append(keys, i18n.MsgAuditExportColumnIPMarker, i18n.MsgAuditExportColumnCoverage)
	}
	for i, key := range keys {
		keys[i] = i18n.Translate(r.job.Lang, key)
	}
	return keys
}

func (r *Reader) Reverse() bool { return true }

func (r *Reader) BatchSize(configured int) int {
	if r.job.AuditFilters.IPRisk != "" {
		return min(configured, 100)
	}
	return min(configured, 1000)
}

func (r *Reader) Read(ctx context.Context, start, end int64, cursor any, limit int) (model.LogExportBatch, error) {
	if !r.job.HasCurrentAuditProjection() {
		return model.LogExportBatch{}, model.ErrLogExportAccessChanged
	}
	f := r.job.AuditFilters.AuditLogFilter
	f.StartTimestamp, f.EndTimestamp = start, end
	position, _ := cursor.(auditsecurity.RiskPosition)
	batch := model.LogExportBatch{}
	var records []*model.AuditLog
	var markers []auditsecurity.IPRiskMarker
	if r.job.AuditFilters.IPRisk != "" {
		if r.revision == nil {
			return batch, model.ErrLogExportRiskChanged
		}
		result, err := auditsecurity.QueryRiskBatch(ctx, r.main, r.logs, f, r.job.CreatorRole, r.job.AuditFilters.IPRisk, *r.revision, position, limit, limit)
		if errors.Is(err, auditsecurity.ErrRiskCursor) {
			return batch, model.ErrLogExportRiskChanged
		}
		if err != nil {
			return batch, err
		}
		batch.Scanned, batch.Done, position = result.Scanned, result.Done, result.Position
		for _, record := range result.Items {
			records = append(records, record.AuditLog)
			markers = append(markers, record.Marker)
		}
	} else {
		q := model.AuditLogQuery(r.logs.WithContext(ctx), f, r.job.CreatorRole).Select(model.AuditLogColumns)
		if position.Event != "" {
			q = q.Where("created_at < ? OR (created_at = ? AND event_id < ?)", position.At, position.At, position.Event)
		}
		if err := q.Order("created_at DESC").Order("event_id DESC").Limit(limit).Find(&records).Error; err != nil {
			return batch, err
		}
		batch.Scanned, batch.Done = len(records), len(records) < limit
		if len(records) > 0 {
			last := records[len(records)-1]
			position = auditsecurity.RiskPosition{At: last.CreatedAt, Event: last.EventId}
		}
	}
	model.ProjectAuditLogs(records, r.job.SelfExport, r.job.CreatorRole)
	batch.Cursor, batch.Position = position, position.At
	for i, row := range records {
		if r.job.SelfExport {
			a := model.ProjectAuditActivity(row)
			target, fields, count, outcome := "", "", "", ""
			if a.Target != nil {
				target = a.Target.Name
				if a.Target.ID != "" {
					target += " (#" + a.Target.ID + ")"
				}
			}
			if a.ChangeSummary != nil {
				fields, count, outcome = strings.Join(a.ChangeSummary.Fields, ", "), strconv.Itoa(a.ChangeSummary.Total), a.ChangeSummary.Outcome
			}
			values := []string{a.EventID, time.Unix(a.CreatedAt, 0).In(r.loc).Format(time.RFC3339), a.Category, a.Action, strconv.FormatBool(a.Success), a.IP, a.Client, a.Source, a.LoginMethod, target, fields, count, outcome, a.RequestID}
			batch.Rows = append(batch.Rows, model.LogExportRow{Values: values, At: row.CreatedAt})
			continue
		}
		other, err := common.Marshal(row.Other)
		if err != nil {
			return batch, err
		}
		employee := ""
		if row.ActorIsEmployee != nil {
			employee = strconv.FormatBool(*row.ActorIsEmployee)
		}
		values := []string{row.EventId, time.Unix(row.CreatedAt, 0).In(r.loc).Format(time.RFC3339), strconv.Itoa(row.UserId), row.Username, strconv.Itoa(row.ActorRole), employee, row.Category, row.Action, row.AuthMethod, row.Ip, row.UserAgent, row.Method, row.Route, strconv.Itoa(row.Status), strconv.FormatBool(row.Success), row.RequestId, row.TokenRef, row.Content, string(other)}
		if markers != nil {
			marker, err := common.Marshal(markers[i])
			if err != nil {
				return batch, err
			}
			coverage, err := common.Marshal(r.coverage)
			if err != nil {
				return batch, err
			}
			values = append(values, string(marker), string(coverage))
		}
		batch.Rows = append(batch.Rows, model.LogExportRow{Values: values, At: row.CreatedAt})
	}
	return batch, nil
}
