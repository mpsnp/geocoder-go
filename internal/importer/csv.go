package importer

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/GameTec-live/geocoder-go/internal/pack"
)

func importCSV(ctx context.Context, builder *pack.Builder, path string, options Options) error {
	input, err := openInput(path)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	buffer := bufio.NewReaderSize(input, 128*1024)
	reader := csv.NewReader(buffer)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	if options.Format == "tsv" || strings.HasSuffix(strings.ToLower(strings.TrimSuffix(path, ".gz")), ".tsv") {
		reader.Comma = '\t'
	} else if comma, err := detectDelimiter(buffer); err == nil {
		reader.Comma = comma
	}
	headers, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read CSV header: %w", err)
	}
	headers = append([]string(nil), headers...)
	for i := range headers {
		headers[i] = normalizedHeader(headers[i])
	}
	line := 1
	for {
		line++
		values, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("CSV line %d: %w", line, err)
		}
		fields := make(map[string]string, len(headers))
		for i, value := range values {
			if i < len(headers) {
				fields[headers[i]] = strings.TrimSpace(value)
			}
		}
		record, err := recordFromStrings(fields)
		if err != nil {
			if options.Strict {
				return fmt.Errorf("CSV line %d: %w", line, err)
			}
			if options.Logf != nil {
				options.Logf("skipping CSV line %d: %v", line, err)
			}
			continue
		}
		if record.Source == "" {
			record.Source = sourceName(path, options)
		}
		if err := addRecord(ctx, builder, record, options); err != nil {
			return fmt.Errorf("CSV line %d: %w", line, err)
		}
	}
	return nil
}

func detectDelimiter(reader *bufio.Reader) (rune, error) {
	line, err := reader.Peek(64 * 1024)
	if err != nil && err != bufio.ErrBufferFull && err != io.EOF {
		return ',', err
	}
	if newline := strings.IndexByte(string(line), '\n'); newline >= 0 {
		line = line[:newline]
	}
	candidates := []rune{',', ';', '\t'}
	best, bestCount := ',', -1
	for _, candidate := range candidates {
		count := strings.Count(string(line), string(candidate))
		if count > bestCount {
			best, bestCount = candidate, count
		}
	}
	return best, nil
}

func normalizedHeader(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "\ufeff")))
	replacer := strings.NewReplacer("-", "_", " ", "_", ":", "_")
	return replacer.Replace(value)
}

func recordFromStrings(fields map[string]string) (pack.Record, error) {
	latText := first(fields, "lat", "latitude", "y")
	lonText := first(fields, "lon", "lng", "longitude", "x")
	lat, err := strconv.ParseFloat(strings.ReplaceAll(latText, ",", "."), 64)
	if err != nil {
		return pack.Record{}, fmt.Errorf("invalid latitude %q", latText)
	}
	lon, err := strconv.ParseFloat(strings.ReplaceAll(lonText, ",", "."), 64)
	if err != nil {
		return pack.Record{}, fmt.Errorf("invalid longitude %q", lonText)
	}
	importance, _ := strconv.ParseFloat(first(fields, "importance", "rank", "confidence"), 64)
	aliasesText := first(fields, "aliases", "alt_names", "alternate_names")
	aliases := strings.FieldsFunc(aliasesText, func(r rune) bool { return r == '|' || r == ';' })
	return pack.Record{
		Source: first(fields, "source"), SourceID: first(fields, "source_id", "id"), Kind: first(fields, "kind", "type"),
		Name: first(fields, "name", "label", "poi"), HouseNumber: first(fields, "house_number", "housenumber", "number", "addr_housenumber"),
		Street: first(fields, "street", "road", "addr_street"), Unit: first(fields, "unit", "addr_unit"),
		Postcode: first(fields, "postcode", "postal_code", "zip", "addr_postcode"), Locality: first(fields, "locality", "city", "town", "village", "postal_city", "addr_city"),
		District: first(fields, "district", "county", "suburb"), Region: first(fields, "region", "state", "province"),
		CountryCode: first(fields, "country_code", "countrycode", "iso2"), Country: first(fields, "country"),
		Latitude: lat, Longitude: lon, Importance: importance, Aliases: aliases, DisplayName: first(fields, "display_name", "formatted", "freeform"),
	}, nil
}

func first(fields map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(fields[key]); value != "" {
			return value
		}
	}
	return ""
}
