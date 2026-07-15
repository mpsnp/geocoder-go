package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/GameTec-live/geocoder-go/internal/pack"
	_ "modernc.org/sqlite"
)

func importPack(ctx context.Context, builder *pack.Builder, path string, options Options) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if output, _ := filepath.Abs(builder.Path()); output == abs {
		return fmt.Errorf("cannot merge a pack into itself")
	}
	dsn := "file:" + filepath.ToSlash(abs) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `
SELECT source,source_id,kind,name,house_number,street,unit,postcode,locality,district,
       region,country_code,country,lat,lon,importance,aliases,display_name
FROM records`)
	if err != nil {
		return fmt.Errorf("source is not a geocoder pack: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record pack.Record
		var aliases string
		if err := rows.Scan(&record.Source, &record.SourceID, &record.Kind, &record.Name, &record.HouseNumber,
			&record.Street, &record.Unit, &record.Postcode, &record.Locality, &record.District, &record.Region,
			&record.CountryCode, &record.Country, &record.Latitude, &record.Longitude, &record.Importance,
			&aliases, &record.DisplayName); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(aliases), &record.Aliases)
		if err := addRecord(ctx, builder, record, options); err != nil {
			return err
		}
	}
	return rows.Err()
}
