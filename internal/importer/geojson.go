package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/GameTec-live/geocoder-go/internal/pack"
)

type geoJSONFeature struct {
	Type       string          `json:"type"`
	ID         any             `json:"id"`
	Geometry   geoJSONGeometry `json:"geometry"`
	Properties map[string]any  `json:"properties"`
}

type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

func importGeoJSON(ctx context.Context, builder *pack.Builder, path string, options Options) error {
	input, err := openInput(path)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	decoder := json.NewDecoder(bufio.NewReaderSize(input, 128*1024))
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := start.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("GeoJSON root must be an object")
	}
	foundFeatures := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key := keyToken.(string)
		if key != "features" {
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return err
			}
			continue
		}
		foundFeatures = true
		arrayStart, err := decoder.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := arrayStart.(json.Delim); !ok || delimiter != '[' {
			return fmt.Errorf("GeoJSON features must be an array")
		}
		for decoder.More() {
			var feature geoJSONFeature
			if err := decoder.Decode(&feature); err != nil {
				return err
			}
			if err := addFeature(ctx, builder, feature, path, options); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return err
		}
	}
	if !foundFeatures {
		return fmt.Errorf("GeoJSON object has no features array; use NDJSON for one feature per line")
	}
	return nil
}

func importNDJSON(ctx context.Context, builder *pack.Builder, path string, options Options) error {
	input, err := openInput(path)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var feature geoJSONFeature
		if err := json.Unmarshal(scanner.Bytes(), &feature); err != nil {
			return fmt.Errorf("NDJSON line %d: %w", line, err)
		}
		if err := addFeature(ctx, builder, feature, path, options); err != nil {
			return fmt.Errorf("NDJSON line %d: %w", line, err)
		}
	}
	return scanner.Err()
}

func addFeature(ctx context.Context, builder *pack.Builder, feature geoJSONFeature, path string, options Options) error {
	lat, lon, err := geometryPoint(feature.Geometry)
	if err != nil {
		if options.Strict {
			return err
		}
		if options.Logf != nil {
			options.Logf("skipping GeoJSON feature: %v", err)
		}
		return nil
	}
	fields := make(map[string]string, len(feature.Properties)+3)
	for key, value := range feature.Properties {
		fields[normalizedHeader(key)] = stringify(value)
	}
	fields["lat"] = strconv.FormatFloat(lat, 'f', -1, 64)
	fields["lon"] = strconv.FormatFloat(lon, 'f', -1, 64)
	if fields["id"] == "" && feature.ID != nil {
		fields["id"] = stringify(feature.ID)
	}
	record, err := recordFromStrings(fields)
	if err != nil {
		return err
	}
	if record.Source == "" {
		record.Source = sourceName(path, options)
	}
	return addRecord(ctx, builder, record, options)
}

func geometryPoint(geometry geoJSONGeometry) (float64, float64, error) {
	if len(geometry.Coordinates) == 0 || string(geometry.Coordinates) == "null" {
		return 0, 0, fmt.Errorf("feature has no geometry")
	}
	if strings.EqualFold(geometry.Type, "Point") {
		var coordinates []float64
		if err := json.Unmarshal(geometry.Coordinates, &coordinates); err != nil || len(coordinates) < 2 {
			return 0, 0, fmt.Errorf("invalid Point coordinates")
		}
		return coordinates[1], coordinates[0], nil
	}
	var coordinates any
	if err := json.Unmarshal(geometry.Coordinates, &coordinates); err != nil {
		return 0, 0, err
	}
	var count int
	var lonSum, latSum float64
	collectCoordinates(coordinates, &lonSum, &latSum, &count)
	if count == 0 {
		return 0, 0, fmt.Errorf("geometry %s has no usable coordinates", geometry.Type)
	}
	return latSum / float64(count), lonSum / float64(count), nil
}

func collectCoordinates(value any, lonSum, latSum *float64, count *int) {
	array, ok := value.([]any)
	if !ok || len(array) == 0 {
		return
	}
	if len(array) >= 2 {
		lon, lonOK := array[0].(float64)
		lat, latOK := array[1].(float64)
		if lonOK && latOK {
			*lonSum += lon
			*latSum += lat
			*count++
			return
		}
	}
	for _, child := range array {
		collectCoordinates(child, lonSum, latSum, count)
	}
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				if value := stringify(object["value"]); value != "" {
					parts = append(parts, value)
				}
				continue
			}
			if value := stringify(item); value != "" {
				parts = append(parts, value)
			}
		}
		return strings.Join(parts, "|")
	default:
		data, _ := json.Marshal(value)
		return string(data)
	}
}
