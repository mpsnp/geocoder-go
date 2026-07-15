package importer

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/GameTec-live/geocoder-go/internal/pack"
)

type Options struct {
	Format       string
	Source       string
	CountryCode  string
	IncludeRoads bool
	Strict       bool
	Logf         func(string, ...any)
}

func Import(ctx context.Context, builder *pack.Builder, input string, options Options) (int64, error) {
	format := strings.ToLower(strings.TrimSpace(options.Format))
	if format == "" || format == "auto" {
		format = detectFormat(input)
	}
	before, _ := builder.Stats()
	var err error
	switch format {
	case "csv", "tsv":
		err = importCSV(ctx, builder, input, options)
	case "geojson", "json":
		err = importGeoJSON(ctx, builder, input, options)
	case "ndjson", "geojsonl", "jsonl":
		err = importNDJSON(ctx, builder, input, options)
	case "pbf", "osm.pbf":
		err = importPBF(ctx, builder, input, options)
	case "sqlite", "sqlite3", "db":
		err = importPack(ctx, builder, input, options)
	case "parquet", "geoparquet":
		err = importOvertureParquet(ctx, builder, input, options)
	default:
		err = fmt.Errorf("cannot determine format for %q; use --format", input)
	}
	after, _ := builder.Stats()
	return after - before, err
}

func detectFormat(path string) string {
	name := strings.ToLower(path)
	name = strings.TrimSuffix(name, ".gz")
	switch {
	case strings.HasSuffix(name, ".osm.pbf") || strings.HasSuffix(name, ".pbf"):
		return "pbf"
	case strings.HasSuffix(name, ".geojsonl") || strings.HasSuffix(name, ".ndjson") || strings.HasSuffix(name, ".jsonl"):
		return "ndjson"
	case strings.HasSuffix(name, ".geojson") || strings.HasSuffix(name, ".json"):
		return "geojson"
	case strings.HasSuffix(name, ".tsv"):
		return "tsv"
	case strings.HasSuffix(name, ".csv"):
		return "csv"
	case strings.HasSuffix(name, ".sqlite3") || strings.HasSuffix(name, ".sqlite") || strings.HasSuffix(name, ".db"):
		return "sqlite"
	case strings.HasSuffix(name, ".geoparquet") || strings.HasSuffix(name, ".parquet"):
		return "parquet"
	default:
		return ""
	}
}

func openInput(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(strings.ToLower(path), ".gz") {
		return file, nil
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{Reader: bufio.NewReaderSize(reader, 128*1024), Closer: multiCloser{reader, file}}, nil
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var first error
	for _, closer := range m {
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func sourceName(input string, options Options) string {
	if options.Source != "" {
		return options.Source
	}
	base := filepath.Base(strings.TrimSuffix(input, ".gz"))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func addRecord(ctx context.Context, builder *pack.Builder, record pack.Record, options Options) error {
	if record.CountryCode == "" {
		record.CountryCode = options.CountryCode
	}
	if record.Source == "" {
		record.Source = options.Source
	}
	if err := builder.Add(ctx, record); err != nil {
		if options.Strict {
			return err
		}
		if options.Logf != nil {
			options.Logf("skipping record: %v", err)
		}
	}
	return nil
}
