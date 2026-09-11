package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GameTec-live/geocoder-go/geocoder"
)

func TestInvalidLocalitiesPreserveOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pack.sqlite")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := createPack(context.Background(), buildFlags{output: path, localities: "missing.geojson"}, nil); err == nil {
		t.Fatal("accepted missing boundaries")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("output changed: %q %v", data, err)
	}
}

func TestLocalityEnrichmentSearch(t *testing.T) {
	dir := t.TempDir()
	boundaries := filepath.Join(dir, "boundaries.geojson")
	input := filepath.Join(dir, "places.csv")
	output := filepath.Join(dir, "pack.sqlite")
	// Synthetic rectangles and development names, not real settlement boundaries.
	files := map[string]string{
		boundaries: `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"locality":"Москва"},"geometry":{"type":"Polygon","coordinates":[[[36,55],[39,55],[39,57],[36,57],[36,55]]]}},{"type":"Feature","properties":{"locality":"Тула"},"geometry":{"type":"Polygon","coordinates":[[[37,53],[39,53],[39,54],[37,54],[37,53]]]}}]}`,
		input:      "source_id,name,lat,lon,kind,locality\nmoscow,Жилой комплекс Пример,55.75,37.6,place,\ntula,ЖК Пример,53.2,37.6,place,\nexplicit,ЖК Образец,55.75,37.6,place,Химки\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := createPack(context.Background(), buildFlags{output: output, localities: boundaries, country: "RU", strict: true}, []string{input}); err != nil {
		t.Fatal(err)
	}
	service, err := geocoder.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for _, tc := range []struct{ query, id string }{
		{"Москва ЖК Пример", "moscow"}, {"Тула жилой комплекс Пример", "tula"},
		{"Химки ЖК Образец", "explicit"}, {"Москва ЖК Образец", ""},
	} {
		results, err := service.Geocode(context.Background(), geocoder.SearchOptions{Query: tc.query, CountryCode: "RU", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if tc.id == "" {
			if len(results) != 0 {
				t.Errorf("%s: unexpected results %#v", tc.query, results)
			}
			continue
		}
		if len(results) != 1 || results[0].SourceID != tc.id {
			t.Errorf("%s: %#v", tc.query, results)
		}
	}
}
