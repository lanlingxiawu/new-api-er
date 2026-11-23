// audit-login-target-repair links only historical failures with stored identity
// proof, or verifies customer assignment evidence with -customer-assignments.
// SQL_DSN must explicitly identify the primary database; no migration or
// implicit .env loading is performed. Without -apply this command only reports.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
)

func main() {
	var options auditsecurity.LoginTargetRepairOptions
	flag.Int64Var(&options.Start, "start", 0, "inclusive Unix timestamp; at most 31 days")
	flag.Int64Var(&options.End, "end", 0, "inclusive Unix timestamp")
	flag.Int64Var(&options.MinID, "min-id", 0, "inclusive minimum alert ID")
	flag.Int64Var(&options.MaxID, "max-id", 0, "inclusive maximum alert ID")
	flag.BoolVar(&options.Apply, "apply", false, "apply verified links; default is report-only")
	flag.BoolVar(&options.CustomerAssignments, "customer-assignments", false, "verify historical password recovery and customer bulk alert assignments")
	flag.Parse()
	if os.Getenv("SQL_DSN") == "" {
		common.SysError("SQL_DSN must explicitly select the maintenance database")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, options); err != nil {
		// Connection errors may contain a DSN. Never print them from this tool.
		common.SysError("Audit identity repair failed; check the selected database, schema, range and connectivity. No further batches were applied.")
		os.Exit(1)
	}
}

func run(ctx context.Context, options auditsecurity.LoginTargetRepairOptions) error {
	db, err := model.OpenAuditSecurityDB(ctx, false)
	if err != nil {
		return err
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	for {
		page, err := auditsecurity.RepairLoginTargetBatch(ctx, db, options)
		if err != nil {
			return err
		}
		raw, err := common.Marshal(page)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(os.Stdout, string(raw)); err != nil {
			return err
		}
		if page.NextID == 0 {
			return nil
		}
		options.CursorAt, options.CursorID = page.NextAt, page.NextID
	}
}
