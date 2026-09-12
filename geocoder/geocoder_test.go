package geocoder_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/GameTec-live/geocoder-go/geocoder"
	"github.com/GameTec-live/geocoder-go/internal/pack"
)

func TestServiceSearchesNestedPacks(t *testing.T) {
	root := t.TempDir()
	makePack(t, filepath.Join(root, "austria", "addresses.sqlite"), []pack.Record{
		{Source: "test", SourceID: "at1", Kind: "address", Street: "Stephansplatz", HouseNumber: "1", Postcode: "1010", Locality: "Wien", CountryCode: "AT", Latitude: 48.20849, Longitude: 16.37208},
		{Source: "test", SourceID: "at2", Kind: "place", Name: "Flughafen Wien", Aliases: []string{"Vienna International Airport"}, Locality: "Schwechat", CountryCode: "AT", Latitude: 48.11028, Longitude: 16.56972},
		{Source: "test", SourceID: "at3", Kind: "address", Street: "Mariahilfer Straße", HouseNumber: "1", Postcode: "1060", Locality: "Wien", CountryCode: "AT", Latitude: 48.20101, Longitude: 16.35978},
		{Source: "test", SourceID: "at4", Kind: "locality", Name: "Freistadt", Locality: "Freistadt", CountryCode: "AT", Latitude: 48.51103, Longitude: 14.50452, Importance: 0.8},
		{Source: "test", SourceID: "at5", Kind: "road", Name: "Altenhofgasse", CountryCode: "AT", Latitude: 48.51212, Longitude: 14.50364, Importance: 0.3},
		{Source: "test", SourceID: "at6", Kind: "road", Name: "Altenhofgasse", CountryCode: "AT", Latitude: 46.7, Longitude: 13.3, Importance: 0.3},
		{Source: "test", SourceID: "at7", Kind: "locality", Name: "Linz", Locality: "Linz", CountryCode: "AT", Latitude: 48.3069, Longitude: 14.2858, Importance: 1},
	})
	makePack(t, filepath.Join(root, "germany", "addresses.db"), []pack.Record{
		{Source: "test", SourceID: "de1", Kind: "address", Street: "Platz der Republik", HouseNumber: "1", Postcode: "10557", Locality: "Berlin", CountryCode: "DE", Latitude: 52.51862, Longitude: 13.37620},
	})

	service, err := geocoder.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	if got := len(service.PackNames()); got != 2 {
		t.Fatalf("got %d packs, want 2", got)
	}

	results, err := service.Geocode(context.Background(), geocoder.SearchOptions{Query: "Stephansplatz 1 Wien", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].SourceID != "at1" {
		t.Fatalf("unexpected geocode results: %#v", results)
	}

	results, err = service.Geocode(context.Background(), geocoder.SearchOptions{Query: "Vienna International Air", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].SourceID != "at2" {
		t.Fatalf("alias/prefix lookup failed: %#v", results)
	}

	for _, test := range []struct {
		query, sourceID string
	}{
		{"Stefansplatz 1 Wien", "at1"},
		{"Mariahilferstrase 1 Wien", "at3"},
		{"Mariahilfer Strasse 1 Wien", "at3"},
		{"Maria hilfer Strasse 1 Wien", "at3"},
		{"Maria hilfer Str asse 1 Wien", "at3"},
	} {
		results, err = service.Geocode(context.Background(), geocoder.SearchOptions{Query: test.query, CountryCode: "AT", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) == 0 || results[0].SourceID != test.sourceID {
			t.Fatalf("typo lookup %q failed: %#v", test.query, results)
		}
	}

	for _, query := range []string{"Altenhofgasse, Freistadt", "Altenhofgasse Freistadt", "Altenhofgasse 3, 4240 Freistadt"} {
		results, err = service.Geocode(context.Background(), geocoder.SearchOptions{Query: query, CountryCode: "AT", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].SourceID != "at5" || results[0].Locality != "Freistadt" || results[0].DisplayName != "Altenhofgasse, Freistadt" {
			t.Fatalf("locality-aware road lookup %q failed: %#v", query, results)
		}
	}
	results, err = service.Geocode(context.Background(), geocoder.SearchOptions{Query: "Altenhofgasse, Linz", CountryCode: "AT", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("distant road was incorrectly assigned to Linz: %#v", results)
	}

	reverse, err := service.Reverse(context.Background(), geocoder.ReverseOptions{Latitude: 48.2085, Longitude: 16.3721, RadiusMeter: 500, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(reverse) != 1 || reverse[0].SourceID != "at1" || reverse[0].DistanceMeter > 10 {
		t.Fatalf("unexpected reverse results: %#v", reverse)
	}
}

func makePack(t *testing.T, path string, records []pack.Record) {
	t.Helper()
	builder, err := pack.Create(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := builder.Add(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := builder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHouseAddressesOnly(t *testing.T) {
	root := t.TempDir()
	records := []pack.Record{
		{SourceID: "house", Kind: "address", Street: "Тверская улица", HouseNumber: "10", Locality: "Москва", Latitude: 55.76, Longitude: 37.61},
		{SourceID: "road", Kind: "road", Name: "Лесная улица", Locality: "Москва", Latitude: 55.76, Longitude: 37.61},
		{SourceID: "incomplete", Kind: "address", Street: "Тверская улица", Locality: "Москва", Latitude: 55.76, Longitude: 37.61},
	}
	for i := 0; i < 110; i++ {
		records = append(records, pack.Record{SourceID: fmt.Sprint("blank", i), Kind: "address", Street: "Тверская улица", HouseNumber: "\t\n\u00a0", Latitude: 55.76 + float64(i)/10000, Longitude: 37.61})
		records = append(records, pack.Record{SourceID: fmt.Sprint(i), Kind: "place", Name: "Тверская улица", Latitude: 55.76 + float64(i)/10000, Longitude: 37.61})
	}
	makePack(t, filepath.Join(root, "test.sqlite"), records)
	svc, err := geocoder.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	for _, query := range []string{"Тверская", "Тверскаа"} {
		results, err := svc.Geocode(context.Background(), geocoder.SearchOptions{Query: query, Limit: 1, HouseAddressesOnly: true})
		if err != nil || len(results) != 1 || results[0].SourceID != "house" {
			t.Fatalf("%s: %#v %v", query, results, err)
		}
	}
	results, err := svc.Geocode(context.Background(), geocoder.SearchOptions{Query: "Лесная Москва", HouseAddressesOnly: true})
	if err != nil || len(results) != 0 {
		t.Fatalf("road fallback: %#v %v", results, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Geocode(ctx, geocoder.SearchOptions{Query: "Тверская", HouseAddressesOnly: true}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestLocalityTypeAndLegacyPacks(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "pack.sqlite")
			makePack(t, path, []pack.Record{{Kind: "address", Street: "Тверская", HouseNumber: "10", Locality: "Example", LocalityType: "ru:станица", Latitude: 1, Longitude: 1}})
			if legacy {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`ALTER TABLE records DROP COLUMN locality_type; PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			svc, err := geocoder.Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			want := "ru:станица"
			if legacy {
				want = ""
			}
			for _, q := range []string{"Тверская", "Тверскаа"} {
				rows, err := svc.Geocode(context.Background(), geocoder.SearchOptions{Query: q, HouseAddressesOnly: true})
				if err != nil || len(rows) != 1 || rows[0].LocalityType != want {
					t.Fatalf("%s: %#v %v", q, rows, err)
				}
			}
			rows, err := svc.Reverse(context.Background(), geocoder.ReverseOptions{Latitude: 1, Longitude: 1})
			if err != nil || len(rows) != 1 || rows[0].LocalityType != want {
				t.Fatalf("reverse: %#v %v", rows, err)
			}
		})
	}
}
