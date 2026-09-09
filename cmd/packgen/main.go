package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GameTec-live/geocoder-go/internal/importer"
	"github.com/GameTec-live/geocoder-go/internal/pack"
	_ "modernc.org/sqlite"
)

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		usage()
		return flag.ErrHelp
	}
	switch arguments[0] {
	case "build":
		return build(arguments[1:])
	case "overture":
		return overture(arguments[1:])
	case "inspect":
		return inspect(arguments[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

type buildFlags struct {
	localities   string
	output       string
	appendMode   bool
	format       string
	source       string
	country      string
	includeRoads bool
	strict       bool
}

func build(arguments []string) error {
	set := flag.NewFlagSet("build", flag.ContinueOnError)
	flags := bindBuildFlags(set)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if flags.output == "" || set.NArg() == 0 {
		return fmt.Errorf("build requires --output and at least one input file")
	}
	return createPack(context.Background(), *flags, set.Args())
}

func overture(arguments []string) error {
	set := flag.NewFlagSet("overture", flag.ContinueOnError)
	flags := bindBuildFlags(set)
	release := set.String("release", "", "Overture release, for example 2026-06-17.0")
	input := set.String("input", "", "override the Overture GeoParquet path or glob")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if flags.output == "" || flags.country == "" {
		return fmt.Errorf("overture requires --output and a two-letter --country")
	}
	if *input == "" {
		if *release == "" {
			return fmt.Errorf("overture requires --release when --input is not supplied")
		}
		*input = fmt.Sprintf("s3://overturemaps-us-west-2/release/%s/theme=addresses/type=address/*", *release)
	}
	flags.format = "parquet"
	if flags.source == "" {
		flags.source = "overture"
	}
	return createPack(context.Background(), *flags, []string{*input})
}

func bindBuildFlags(set *flag.FlagSet) *buildFlags {
	values := new(buildFlags)
	set.StringVar(&values.localities, "localities", "", "GeoJSON settlement polygons used to fill missing locality tags")
	set.StringVar(&values.output, "output", "", "output SQLite pack")
	set.BoolVar(&values.appendMode, "append", false, "append to an existing pack and rebuild its indexes")
	set.StringVar(&values.format, "format", "auto", "input format: auto, csv, tsv, geojson, ndjson, pbf, sqlite, parquet")
	set.StringVar(&values.source, "source", "", "source attribution stored on imported records")
	set.StringVar(&values.country, "country", "", "fallback/filter ISO 3166-1 alpha-2 country code")
	set.BoolVar(&values.includeRoads, "include-roads", true, "include named highway ways from OSM PBF files (use --include-roads=false for compact packs)")
	set.BoolVar(&values.strict, "strict", false, "abort on the first malformed record")
	return values
}

func createPack(ctx context.Context, flags buildFlags, inputs []string) (err error) {
	started := time.Now()
	var localities *importer.Localities
	if flags.localities != "" {
		localities, err = importer.LoadLocalities(flags.localities)
		if err != nil {
			return err
		}
	}
	builder, err := pack.Create(ctx, flags.output, flags.appendMode)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		_ = builder.Close()
		if !success && !flags.appendMode {
			_ = os.Remove(flags.output)
		}
	}()
	options := importer.Options{
		Localities: localities, Format: flags.format, Source: flags.source, CountryCode: strings.ToUpper(flags.country),
		IncludeRoads: flags.includeRoads, Strict: flags.strict, Logf: log.Printf,
	}
	if localities != nil {
		if err := builder.SetMetadata(ctx, "locality_boundaries_sha256", localities.SHA256); err != nil {
			return err
		}
	}
	for _, input := range inputs {
		log.Printf("importing %s", input)
		count, err := importer.Import(ctx, builder, input, options)
		if err != nil {
			return fmt.Errorf("import %s: %w", input, err)
		}
		log.Printf("imported %d new records from %s", count, input)
	}
	if err := builder.SetMetadata(ctx, "country", options.CountryCode); err != nil {
		return err
	}
	log.Printf("building FTS5 and spatial indexes")
	if err := builder.Finalize(ctx); err != nil {
		return err
	}
	inserted, skipped := builder.Stats()
	success = true
	log.Printf("created %s with %d new records (%d skipped) in %s", flags.output, inserted, skipped, time.Since(started).Round(time.Millisecond))
	return nil
}

func inspect(arguments []string) error {
	set := flag.NewFlagSet("inspect", flag.ContinueOnError)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return fmt.Errorf("inspect requires exactly one pack path")
	}
	abs, err := filepath.Abs(set.Arg(0))
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT key,value FROM metadata ORDER BY key`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", key, value)
	}
	_ = rows.Close()
	rows, err = db.Query(`SELECT kind,count(*) FROM records GROUP BY kind ORDER BY kind`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			return err
		}
		fmt.Printf("records.%s: %d\n", kind, count)
	}
	return rows.Err()
}

func usage() {
	fmt.Fprintln(os.Stderr, `AtlasTaxi embedded geocoder pack generator

Usage:
  packgen build [flags] <input> [input...]
  packgen overture --output <pack> --country <CC> --release <release>
  packgen inspect <pack>

Run "packgen <command> -h" for command-specific flags.`)
}
