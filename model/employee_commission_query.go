package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm/clause"
)

const CommissionLogMaxRangeSeconds = int64(31 * 86400)

var ErrInvalidCommissionLogQuery = errors.New("invalid commission log query")

type CommissionLogCursor struct {
	CreatedAt int64 `json:"created_at"`
	Id        int   `json:"id"`
}

type CommissionLogPageFilter struct {
	CommissionLogFilter
	Cursor *CommissionLogCursor
}

func (filter CommissionLogPageFilter) Validate() error {
	if filter.StartTime <= 0 || filter.EndTime < filter.StartTime || filter.EndTime-filter.StartTime > CommissionLogMaxRangeSeconds ||
		filter.PageSize < 1 || filter.PageSize > 100 || filter.EmployeeUserId < 0 || filter.CustomerUserId < 0 || filter.ChannelId < 0 ||
		len([]rune(filter.ModelName)) > 255 {
		return ErrInvalidCommissionLogQuery
	}
	switch filter.LossStatus {
	case "", "loss", "normal":
	default:
		return ErrInvalidCommissionLogQuery
	}
	if cursor := filter.Cursor; cursor != nil && (cursor.Id <= 0 || cursor.CreatedAt < filter.StartTime || cursor.CreatedAt > filter.EndTime) {
		return ErrInvalidCommissionLogQuery
	}
	return nil
}

type CommissionLogPage struct {
	Items      []*EmployeeCommissionLog `json:"items"`
	HasMore    bool                     `json:"has_more"`
	NextCursor *CommissionLogCursor     `json:"next_cursor"`
}

// ListCommissionLogs reads retained details without depending on historical
// aggregates, cleanup receipts, or an exact count of the matching rows.
func ListCommissionLogs(ctx context.Context, filter CommissionLogPageFilter) (CommissionLogPage, error) {
	filter.ModelName = strings.TrimSpace(filter.ModelName)
	filter.LossStatus = strings.TrimSpace(filter.LossStatus)
	if err := filter.Validate(); err != nil {
		return CommissionLogPage{}, err
	}
	ctx, cancel := context.WithTimeout(safeDBContext(ctx), 10*time.Second)
	defer cancel()
	if filter.Cursor != nil {
		// Bound the index scan as well as the tie-breaking predicate. PostgreSQL
		// can otherwise scan newer rows before evaluating the OR below.
		filter.EndTime = filter.Cursor.CreatedAt
	}
	query := applyCommissionLogFilter(DB.WithContext(ctx).Model(&EmployeeCommissionLog{}), filter.CommissionLogFilter)
	if DB.Dialector.Name() == "mysql" {
		// Closing the client connection does not reliably stop a MySQL scan.
		// Bound server work too, including an earlier parent request deadline.
		deadline, _ := ctx.Deadline()
		maxExecutionMS := max(int64(1), time.Until(deadline).Milliseconds())
		query = query.Clauses(clause.Select{})
		selectClause := query.Statement.Clauses["SELECT"]
		selectClause.AfterNameExpression = clause.Expr{SQL: "/*+ MAX_EXECUTION_TIME(" + strconv.FormatInt(maxExecutionMS, 10) + ") */"}
		query.Statement.Clauses["SELECT"] = selectClause
	}
	if DB.Dialector.Name() == "mysql" && filter.ChannelId > 0 && filter.EmployeeUserId == 0 && filter.CustomerUserId == 0 {
		// MySQL can prefer the time-only index for LIMIT and then fetch every
		// row in an empty channel window. Push the channel check into its index.
		// Keep employee/customer lookups available for their narrower scopes.
		query = query.Clauses(clause.From{})
		from := query.Statement.Clauses["FROM"]
		from.AfterExpression = clause.Expr{SQL: "FORCE INDEX (`idx_employee_commission_created_channel`)"}
		query.Statement.Clauses["FROM"] = from
	}
	if cursor := filter.Cursor; cursor != nil {
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.Id)
	}
	rows := make([]*EmployeeCommissionLog, 0, filter.PageSize+1)
	err := query.Select("id, employee_id, employee_user_id, customer_user_id, log_id, model_name, channel_id, revenue_quota, cost_quota, profit_quota, commission_quota, commission_rate, cost_ratio, group_ratio, settle_status, settled_at, created_at").
		Order("created_at DESC").Order("id DESC").Limit(filter.PageSize + 1).Find(&rows).Error
	if err != nil {
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 3024 {
			return CommissionLogPage{}, errors.Join(context.DeadlineExceeded, err)
		}
		return CommissionLogPage{}, err
	}
	page := CommissionLogPage{Items: rows, HasMore: len(rows) > filter.PageSize}
	if page.HasMore {
		page.Items = rows[:filter.PageSize]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &CommissionLogCursor{CreatedAt: last.CreatedAt, Id: last.Id}
	}
	return page, nil
}
