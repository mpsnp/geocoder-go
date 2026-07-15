package pack

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Builder struct {
	path     string
	db       *sql.DB
	tx       *sql.Tx
	insert   *sql.Stmt
	inserted int64
	skipped  int64
}

func Create(ctx context.Context, path string, appendMode bool) (*Builder, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if !appendMode {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, createBaseSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create pack schema: %w", err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(SchemaVersion)); err != nil {
		_ = db.Close()
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT OR IGNORE INTO records(
  source, source_id, kind, name, house_number, street, unit, postcode,
  locality, district, region, country_code, country, lat, lon, importance,
  aliases, display_name, search_text, fingerprint
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		return nil, err
	}
	return &Builder{path: abs, db: db, tx: tx, insert: stmt}, nil
}

func (b *Builder) Add(ctx context.Context, record Record) error {
	if err := record.Prepare(); err != nil {
		b.skipped++
		return err
	}
	aliases, _ := json.Marshal(record.Aliases)
	result, err := b.insert.ExecContext(ctx,
		record.Source, record.SourceID, record.Kind, record.Name, record.HouseNumber,
		record.Street, record.Unit, record.Postcode, record.Locality, record.District,
		record.Region, record.CountryCode, record.Country, record.Latitude, record.Longitude,
		record.Importance, string(aliases), record.DisplayName, record.SearchText, record.Fingerprint,
	)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n > 0 {
		b.inserted++
	} else {
		b.skipped++
	}
	return nil
}

func (b *Builder) SetMetadata(ctx context.Context, key, value string) error {
	_, err := b.tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (b *Builder) Finalize(ctx context.Context) error {
	if b.insert != nil {
		if err := b.insert.Close(); err != nil {
			return err
		}
		b.insert = nil
	}
	if b.tx != nil {
		if err := b.tx.Commit(); err != nil {
			return err
		}
		b.tx = nil
	}
	if _, err := b.db.ExecContext(ctx, createSearchSchema); err != nil {
		return fmt.Errorf("build search indexes: %w", err)
	}
	var total int64
	if err := b.db.QueryRowContext(ctx, `SELECT count(*) FROM records`).Scan(&total); err != nil {
		return err
	}
	metadata := map[string]string{
		"built_at":     time.Now().UTC().Format(time.RFC3339),
		"record_count": strconv.FormatInt(total, 10),
	}
	for key, value := range metadata {
		if _, err := b.db.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			return err
		}
	}
	if _, err := b.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	return nil
}

func (b *Builder) Close() error {
	var errors []string
	if b.insert != nil {
		_ = b.insert.Close()
	}
	if b.tx != nil {
		_ = b.tx.Rollback()
	}
	if b.db != nil {
		if err := b.db.Close(); err != nil {
			errors = append(errors, err.Error())
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("close builder: %s", strings.Join(errors, "; "))
	}
	return nil
}

func (b *Builder) Path() string          { return b.path }
func (b *Builder) Stats() (int64, int64) { return b.inserted, b.skipped }
func (b *Builder) DB() *sql.DB           { return b.db }
func (b *Builder) Tx() *sql.Tx           { return b.tx }
