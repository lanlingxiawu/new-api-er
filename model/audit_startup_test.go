package model

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// The endpoint accepts real TCP connections but never completes a DB handshake.
// It receives no usable credentials and does not emulate database queries.
func stalledAuditEndpoint(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var connections []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections = append(connections, conn)
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = listener.Close()
			<-done
			for _, conn := range connections {
				_ = conn.Close()
			}
		})
	}
	t.Cleanup(stop)
	return listener.Addr().String(), stop
}

func TestAuditPoolInitializationBoundsHandshake(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		for _, kind := range []string{"read", "export"} {
			t.Run(dialect+"/"+kind, func(t *testing.T) {
				address, stop := stalledAuditEndpoint(t)
				dsn := "probe@tcp(" + address + ")/probe"
				if dialect == "postgres" {
					dsn = "postgres://probe@" + address + "/probe?sslmode=disable"
				}
				t.Setenv("SQL_DSN", dsn)
				t.Setenv("LOG_SQL_DSN", dsn)
				t.Cleanup(CloseAuditReadPools)
				t.Cleanup(CloseAuditExportPools)
				done := make(chan error, 1)
				go func() {
					if kind == "read" {
						done <- InitAuditReadPools(t.Context())
					} else {
						done <- InitAuditExportPools(t.Context())
					}
				}()
				select {
				case err := <-done:
					require.Error(t, err, "unresponsive storage must not be published as ready")
				case <-time.After(6 * time.Second):
					stop()
					select {
					case <-done:
					case <-time.After(4 * time.Second):
						t.Fatal("initializer did not stop after the endpoint closed")
					}
					t.Fatal("pool initialization exceeded its total connection budget")
				}
				if kind == "read" {
					_, err := AuditReadDB(false)
					require.Error(t, err)
				} else {
					_, err := AuditExportDB(false)
					require.Error(t, err)
				}
			})
		}
	}
}

func TestAuditPoolsDoNotMigrateConfiguredDatabases(t *testing.T) {
	dsn, logDSN := os.Getenv("AUDIT_STARTUP_MAIN_DSN"), os.Getenv("AUDIT_STARTUP_LOG_DSN")
	if dsn == "" || logDSN == "" {
		t.Skip("isolated empty audit startup databases required")
	}
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", logDSN)
	for _, logStore := range []bool{false, true} {
		db, err := OpenAuditSecurityDB(t.Context(), logStore)
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		tables, err := db.Migrator().GetTables()
		require.NoError(t, err)
		require.Empty(t, tables, "use new disposable databases, never a business database")
		require.NoError(t, pool.Close())
	}
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		require.NoError(t, InitAuditReadPools(ctx))
		require.NoError(t, InitAuditExportPools(ctx))
		cancel() // Successful pools must not retain the initialization context.
		t.Cleanup(CloseAuditReadPools)
		t.Cleanup(CloseAuditExportPools)
		for _, logStore := range []bool{false, true} {
			reader, err := AuditReadDB(logStore)
			require.NoError(t, err)
			tables, err := reader.Migrator().GetTables()
			require.NoError(t, err)
			require.Empty(t, tables, "query pool initialization must not migrate the database")
			exporter, err := AuditExportDB(logStore)
			require.NoError(t, err)
			var value int
			require.NoError(t, exporter.Raw("SELECT 1").Scan(&value).Error)
			require.Equal(t, 1, value)
			sleepSQL := "SELECT SLEEP(4)"
			if exporter.Dialector.Name() == "postgres" {
				sleepSQL = "SELECT pg_sleep(4)"
			}
			queryCtx, queryCancel := context.WithTimeout(t.Context(), 6*time.Second)
			require.NoError(t, exporter.WithContext(queryCtx).Exec(sleepSQL).Error, "exports can exceed the initialization budget")
			queryCancel()
		}
		CloseAuditReadPools()
		CloseAuditExportPools()
	}
}

func TestAuditPoolInitializationHonorsCancellation(t *testing.T) {
	for _, initialize := range []func(context.Context) error{InitAuditReadPools, InitAuditExportPools} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		t.Setenv("SQL_DSN", "invalid DSN")
		require.ErrorIs(t, initialize(ctx), context.Canceled, "cancel before parsing or connecting")
	}
}

func TestAuditPoolFailureClosesOpenedConnections(t *testing.T) {
	dsn := os.Getenv("AUDIT_STARTUP_MAIN_DSN")
	if dsn == "" {
		t.Skip("isolated audit startup database required")
	}
	require.Contains(t, dsn, "/audit_test_startup_")
	t.Setenv("SQL_DSN", dsn)
	observer, err := OpenAuditSecurityDB(t.Context(), false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closeDB(observer)) })
	countSQL := "SELECT count(*) FROM information_schema.processlist WHERE db = DATABASE()"
	if observer.Dialector.Name() == "postgres" {
		countSQL = "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()"
	}
	var baseline int64
	require.NoError(t, observer.Raw(countSQL).Scan(&baseline).Error)
	for _, kind := range []string{"read", "export"} {
		t.Run(kind, func(t *testing.T) {
			address, _ := stalledAuditEndpoint(t)
			t.Setenv("LOG_SQL_DSN", "probe@tcp("+address+")/probe")
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			start := time.Now()
			if kind == "read" {
				err = InitAuditReadPools(ctx)
				require.Nil(t, auditReaders.Load())
			} else {
				err = InitAuditExportPools(ctx)
				require.Nil(t, auditExportReaders.Load())
			}
			require.Error(t, err)
			require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
			require.Less(t, time.Since(start), time.Second, "respect the caller's shorter budget")
			require.Eventually(t, func() bool {
				var count int64
				return observer.Raw(countSQL).Scan(&count).Error == nil && count == baseline
			}, 2*time.Second, 20*time.Millisecond, "the first pool must close when the log connection fails")
		})
	}
}

func TestAuditSecurityMigrationUsesMasterStartup(t *testing.T) {
	dsn := os.Getenv("AUDIT_STARTUP_MIGRATION_DSN")
	if dsn == "" {
		t.Skip("isolated empty startup migration database required")
	}
	require.Contains(t, dsn, "/audit_test_startup_")
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", dsn)
	previousDB, previousMaster := DB, common.IsMasterNode
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		if DB != previousDB {
			require.NoError(t, closeDB(DB))
		}
		DB, common.IsMasterNode = previousDB, previousMaster
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
		initCol()
	})
	probe, err := OpenAuditSecurityDB(t.Context(), false)
	require.NoError(t, err)
	tables, err := probe.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "use a fresh disposable database")
	require.NoError(t, closeDB(probe))
	common.IsMasterNode = false
	require.NoError(t, InitDB())
	require.False(t, DB.Migrator().HasTable(&SecurityAlert{}), "replicas do not migrate security tables")
	require.NoError(t, closeDB(DB))
	common.IsMasterNode = true
	require.NoError(t, InitDB())
	require.True(t, DB.Migrator().HasTable(&SecurityAlert{}))
	var state SecurityRiskState
	require.NoError(t, DB.Where("name = ?", "risk").Take(&state).Error)
	recorder := &migrationSQLRecorder{}
	DB.Logger = recorder
	require.NoError(t, migrateDB())
	for _, statement := range recorder.schemaMutations() {
		require.NotContains(t, strings.ToLower(statement), "audit_security", "second startup must not alter security tables")
	}
}
