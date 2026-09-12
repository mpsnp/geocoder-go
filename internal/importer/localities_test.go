package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameTec-live/geocoder-go/internal/pack"
)

func boundaryFile(t *testing.T, geometry string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "localities.geojson")
	data := `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"locality":"Example City"},"geometry":` + geometry + `}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalitiesContainment(t *testing.T) {
	path := boundaryFile(t, `{"type":"MultiPolygon","coordinates":[[[[0,0],[2,0],[2,2],[0,2],[0,0]],[[0.2,0.2],[0.4,0.2],[0.4,0.4],[0.2,0.4],[0.2,0.2]]],[[[3,0],[4,0],[4,1],[3,1],[3,0]]]]}`)
	boundaries, err := LoadLocalities(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		lon, lat       float64
		existing, want string
	}{
		{1, 1, "", "Example City"}, {0.3, 0.3, "", ""}, {3.5, 0.5, "", "Example City"},
		{5, 1, "", ""}, {1, 1, "Explicit City", "Explicit City"}, {0, 1, "", "Example City"},
	} {
		r := pack.Record{Longitude: tc.lon, Latitude: tc.lat, Locality: tc.existing}
		boundaries.Enrich(&r)
		if r.Locality != tc.want {
			t.Errorf("%#v: got %q", tc, r.Locality)
		}
	}
	if len(boundaries.SHA256) != 64 {
		t.Fatal("missing checksum")
	}
}

func TestLocalitiesOverlap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overlap.geojson")
	feature := func(name string) string {
		return `{"type":"Feature","properties":{"locality":"` + name + `"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}}`
	}
	for _, names := range [][2]string{{"A", "A"}, {"A", "B"}} {
		if err := os.WriteFile(path, []byte(`{"type":"FeatureCollection","features":[`+feature(names[0])+`,`+feature(names[1])+`]}`), 0600); err != nil {
			t.Fatal(err)
		}
		boundaries, err := LoadLocalities(path)
		if err != nil {
			t.Fatal(err)
		}
		r := pack.Record{Longitude: 1, Latitude: 1}
		boundaries.Enrich(&r)
		want := "A"
		if names[0] != names[1] {
			want = ""
		}
		if r.Locality != want {
			t.Fatalf("overlap %v: %q", names, r.Locality)
		}
	}
}

func TestLocalitiesRejectMalformedGeometry(t *testing.T) {
	for _, geometry := range []string{
		`{"type":"Polygon","coordinates":[[[null,0],[2,0],[2,2],[null,0]]]}`,
		`null`, `{"type":"Point","coordinates":[1,1]}`, `{"type":"Polygon","coordinates":[]}`,
		`{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2]]]}`,
		`{"type":"Polygon","coordinates":[[[0,94],[2,0],[2,2],[0,94]]]}`,
		`{"type":"Polygon","coordinates":[[[0],[2,0],[2,2],[0]]]}`,
	} {
		if _, err := LoadLocalities(boundaryFile(t, geometry)); err == nil {
			t.Errorf("accepted %s", geometry)
		}
	}
}

func TestOfficialLocalityType(t *testing.T) {
	path := boundaryFile(t, `{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}`)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), `"locality":"Example City"`, `"locality":"Example City","official_status":"ru:станица"`))
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	boundaries, err := LoadLocalities(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ locality, initial, wantName, wantType string }{
		{"", "", "Example City", "ru:станица"}, {"Example City", "", "Example City", "ru:станица"}, {"Other", "", "Other", ""}, {"Example City", "ru:город", "Example City", "ru:город"},
	} {
		r := pack.Record{Locality: tc.locality, LocalityType: tc.initial, Latitude: 1, Longitude: 1}
		boundaries.Enrich(&r)
		if r.Locality != tc.wantName || r.LocalityType != tc.wantType {
			t.Fatalf("%+v: %+v", tc, r)
		}
	}
	// Conflicting official types for the same polygon name must not depend on order.
	other := boundaries.polygons[0]
	other.officialStatus = "ru:город"
	boundaries.polygons = append(boundaries.polygons, other)
	r := pack.Record{Latitude: 1, Longitude: 1}
	boundaries.Enrich(&r)
	if r.LocalityType != "" {
		t.Fatalf("conflicting type: %+v", r)
	}
}
