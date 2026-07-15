package importer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/GameTec-live/geocoder-go/internal/pack"
)

// importOvertureParquet uses DuckDB as a build-time adapter. DuckDB is not a
// runtime dependency of the generated pack or geocoder server.
func importOvertureParquet(ctx context.Context, builder *pack.Builder, input string, options Options) error {
	duckdb, err := exec.LookPath("duckdb")
	if err != nil {
		return fmt.Errorf("importing GeoParquet requires the duckdb CLI in PATH: %w", err)
	}
	temp, err := os.CreateTemp("", "atlastaxi-overture-*.csv")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()

	filter := ""
	if country := strings.ToUpper(strings.TrimSpace(options.CountryCode)); country != "" {
		filter = " AND country='" + sqlLiteral(country) + "'"
	}
	query := fmt.Sprintf(`
INSTALL spatial; LOAD spatial;
INSTALL httpfs; LOAD httpfs;
SET s3_region='us-west-2';
COPY (
  SELECT
    id AS source_id,
    'address' AS kind,
    '' AS name,
    COALESCE(number, '') AS house_number,
    COALESCE(street, '') AS street,
    COALESCE(unit, '') AS unit,
    COALESCE(postcode, '') AS postcode,
    COALESCE(postal_city, list_extract(list_transform(address_levels, x -> x.value), -1), '') AS locality,
    '' AS district,
    COALESCE(list_extract(list_transform(address_levels, x -> x.value), 1), '') AS region,
    COALESCE(country, '') AS country_code,
    ST_Y(geometry) AS lat,
    ST_X(geometry) AS lon,
    1.0 AS importance
  FROM read_parquet('%s', hive_partitioning=true)
  WHERE geometry IS NOT NULL%s
) TO '%s' (FORMAT CSV, HEADER true);`, sqlLiteral(filepath.ToSlash(input)), filter, sqlLiteral(filepath.ToSlash(tempPath)))
	command := exec.CommandContext(ctx, duckdb, "-c", query)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("DuckDB Overture conversion failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	childOptions := options
	childOptions.Format = "csv"
	if childOptions.Source == "" {
		childOptions.Source = "overture"
	}
	return importCSV(ctx, builder, tempPath, childOptions)
}

func sqlLiteral(value string) string { return strings.ReplaceAll(value, "'", "''") }
