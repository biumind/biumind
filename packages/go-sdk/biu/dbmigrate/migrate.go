// Package dbmigrate runs goose migrations on service startup, with
// auto-baseline for adopting goose on pre-existing databases.
//
// Why this exists: services own .sql files under <service>/migrations
// formatted with goose Up/Down markers. Without a runner the operator
// has to apply them by hand — and feeding goose-formatted SQL straight
// to psql executes BOTH Up and Down (the markers are comments to psql),
// which silently undoes Up. We fix that by running goose proper, with
// one piece of glue logic: when a service first adopts dbmigrate against
// an existing database (no <svc>_goose_db_version table yet, but the
// schema is already developed), we mark all current migration versions
// as applied so goose doesn't try to re-apply them.
//
// Usage from a service main.go:
//
//	if err := dbmigrate.Run(ctx, pool,
//	    "identity",                                    // service name
//	    "/etc/biumind/migrations/identity",            // dir
//	    "identity.users",                              // check table
//	    8); err != nil {                               // baseline max version
//	    return fmt.Errorf("migrate: %w", err)
//	}
//
// The `service` arg drives a per-service goose tracking table named
// `<service>_goose_db_version`. Earlier versions of this package used
// the default `public.goose_db_version` table — disastrous in a shared
// database, because every service's goose run polluted the same row
// space and "version 3" applied by service A would be reported as
// applied by service B too, silently skipping B's actual 00003 SQL.
// The per-service table fixes this categorically. See
// MigrateLegacyTracking below for moving an existing shared table into
// per-service tables when an old deployment has accumulated such drift.
//
// The `checkTable` is "<schema>.<table>" of an artifact created by the
// very first migration. If that table exists but the goose tracking
// table doesn't, we treat the DB as pre-existing and bootstrap;
// otherwise (fresh DB) goose Up applies everything from scratch.
//
// `baselineMaxVersion` decides which versions get marked applied at
// baseline time. See the doc on Run.
package dbmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// validServiceName matches the same character set as a Go identifier
// minus uppercase: short, lowercase, ascii-only. We embed the value
// directly into a SQL identifier (`<svc>_goose_db_version`) so any
// non-conforming input would be a SQL injection vector. Reject early.
var validServiceName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// Run executes goose Up against pool's database, with auto-baseline.
// dir must be the absolute path to the goose migrations directory
// inside the running container (or at runtime — for local dev, an
// absolute repo path works).
//
// service names the per-service goose tracking table:
// `<service>_goose_db_version`. Must match validServiceName (lowercase
// identifier, 1-31 chars). Two services on the same DB MUST pass
// different service names — otherwise their version tracking races
// and one's migrations get silently skipped. Production names should
// match each binary's serviceName (e.g. "identity", "runtime",
// "brain").
//
// checkTable is "<schema>.<table>" of an artifact from migration 00001.
// Used only to decide whether a goose-less existing DB needs baselining.
//
// baselineMaxVersion 决定 baseline 时只把 version <= 这个值的 migration
// mark 成 applied. 用途: 服务首次切到 dbmigrate 包时, 假设当时 db 已
// 跑过的 migration 文件最多到这个版本号, 之后新加的 migration (版本号
// 更高) 才能正常 goose up. 0 = 把所有当前文件全 mark applied (老行为,
// 仅当首次切包时 dir 里只有"已应用过的" 文件时才安全).
//
// 例: identity 服务首次切 dbmigrate 时 db 已跑过 00001..00008, 之后又
// 加了 00009/00010/00011 — 必须传 baselineMaxVersion=8, 否则 baseline
// 会把 00009-00011 也 mark 成 applied, 但表实际没建.
func Run(ctx context.Context, pool *pgxpool.Pool, service, dir, checkTable string, baselineMaxVersion int64) error {
	if !validServiceName.MatchString(service) {
		return fmt.Errorf("dbmigrate: service name %q invalid; want lowercase ident", service)
	}
	tableName := service + "_goose_db_version"
	// goose has a global table-name setter — set it before any goose
	// call below. There's no per-call override; the global is fine
	// because each Run call is a one-shot at process boot.
	goose.SetTableName(tableName)

	// goose's API takes *sql.DB. Wrap pgx pool — shares the same
	// connections, no second pool / no extra DSN parsing.
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	bootstrap, err := needsBaseline(ctx, db, tableName, checkTable)
	if err != nil {
		return fmt.Errorf("check baseline: %w", err)
	}
	if bootstrap {
		slog.Info("dbmigrate: baselining pre-existing database",
			"service", service, "table", tableName,
			"dir", dir, "check_table", checkTable,
			"baseline_max_version", baselineMaxVersion)
		if err := baseline(ctx, db, tableName, dir, baselineMaxVersion); err != nil {
			return fmt.Errorf("baseline: %w", err)
		}
	}

	before, _ := goose.GetDBVersionContext(ctx, db)
	if err := goose.UpContext(ctx, db, dir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	after, _ := goose.GetDBVersionContext(ctx, db)
	slog.Info("dbmigrate: ok",
		"service", service, "table", tableName,
		"dir", dir, "from", before, "to", after)
	return nil
}

// needsBaseline returns true when the per-service goose tracking
// table does not exist yet AND the checkTable is already present
// (= existing DB without goose tracking for this service). Fresh DBs
// return false → goose Up runs all migrations.
func needsBaseline(ctx context.Context, db *sql.DB, gooseTable, checkTable string) (bool, error) {
	var hasGoose bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)
	`, gooseTable).Scan(&hasGoose); err != nil {
		return false, err
	}
	if hasGoose {
		return false, nil
	}
	parts := strings.SplitN(checkTable, ".", 2)
	if len(parts) != 2 {
		return false, fmt.Errorf("checkTable must be schema.table, got %q", checkTable)
	}
	var hasCheck bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = $1 AND table_name = $2
		)
	`, parts[0], parts[1]).Scan(&hasCheck); err != nil {
		return false, err
	}
	return hasCheck, nil
}

// baseline creates the per-service tracking table and marks migrations
// <= maxVersion in dir as applied. maxVersion=0 marks all (legacy
// behavior; safe only when dir contains only already-applied files at
// the first-baseline moment — adding new migrations after that without
// bumping maxVersion will silently mark them applied without running).
//
// Idempotent — re-running on an already-baselined DB is a no-op because
// we filter inserts by version_id.
//
// gooseTable is parameterised but interpolated as a literal SQL
// identifier (not a $1 bind), since Postgres won't accept a placeholder
// for a table name. Run validates the service name up front so the
// interpolation is safe.
func baseline(ctx context.Context, db *sql.DB, gooseTable, dir string, maxVersion int64) error {
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id          SERIAL PRIMARY KEY,
			version_id  BIGINT NOT NULL,
			is_applied  BOOLEAN NOT NULL,
			tstamp      TIMESTAMP DEFAULT now()
		)
	`, gooseTable)
	if _, err := db.ExecContext(ctx, createSQL); err != nil {
		return err
	}

	// goose 约定: version 0 = baseline marker. Insert if missing.
	zeroSQL := fmt.Sprintf(`
		INSERT INTO %s (version_id, is_applied)
		SELECT 0, true
		WHERE NOT EXISTS (SELECT 1 FROM %s WHERE version_id = 0)
	`, gooseTable, gooseTable)
	if _, err := db.ExecContext(ctx, zeroSQL); err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %q: %w", dir, err)
	}
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (version_id, is_applied)
		SELECT $1, true
		WHERE NOT EXISTS (SELECT 1 FROM %s WHERE version_id = $1)
	`, gooseTable, gooseTable)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		idx := strings.IndexByte(name, '_')
		if idx <= 0 {
			continue
		}
		v, err := strconv.ParseInt(name[:idx], 10, 64)
		if err != nil {
			continue
		}
		// maxVersion=0 = legacy "mark all"; >0 = only mark <= maxVersion.
		if maxVersion > 0 && v > maxVersion {
			continue
		}
		if _, err := db.ExecContext(ctx, insertSQL, v); err != nil {
			return fmt.Errorf("baseline %s: %w", name, err)
		}
	}
	return nil
}
