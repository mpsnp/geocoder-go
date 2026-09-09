package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/GameTec-live/geocoder-go/internal/pack"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/planar"
)

type localityPolygon struct {
	name    string
	polygon orb.Polygon
	bound   orb.Bound
}

// Localities contains build-time WGS84 settlement boundaries, never runtime data.
type Localities struct {
	SHA256   string
	polygons []localityPolygon
}

// LoadLocalities validates a Polygon/MultiPolygon FeatureCollection before a pack
// is opened. Each feature must identify an actual settlement in properties.locality.
func LoadLocalities(path string) (*Localities, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var collection struct {
		Type     string `json:"type"`
		Features []struct {
			Type       string `json:"type"`
			Properties struct {
				Locality string `json:"locality"`
			} `json:"properties"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &collection); err != nil {
		return nil, fmt.Errorf("localities: %w", err)
	}
	if collection.Type != "FeatureCollection" || len(collection.Features) == 0 {
		return nil, fmt.Errorf("localities: expected a nonempty FeatureCollection")
	}
	result := &Localities{SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	for i, feature := range collection.Features {
		name := strings.TrimSpace(feature.Properties.Locality)
		if feature.Type != "Feature" || name == "" {
			return nil, fmt.Errorf("localities feature %d: missing type or locality", i)
		}
		// encoding/json otherwise accepts null as a zero-valued float.
		if bytes.Contains(feature.Geometry.Coordinates, []byte("null")) {
			return nil, fmt.Errorf("localities feature %d: null coordinate", i)
		}
		// Decode positions as slices so malformed one- or three-dimensional positions
		// cannot silently become a different point through fixed-array decoding.
		var coordinates [][][][]float64
		switch feature.Geometry.Type {
		case "Polygon":
			var polygon [][][]float64
			err = json.Unmarshal(feature.Geometry.Coordinates, &polygon)
			coordinates = append(coordinates, polygon)
		case "MultiPolygon":
			err = json.Unmarshal(feature.Geometry.Coordinates, &coordinates)
		default:
			return nil, fmt.Errorf("localities feature %d: expected Polygon or MultiPolygon", i)
		}
		if err != nil || len(coordinates) == 0 {
			return nil, fmt.Errorf("localities feature %d: invalid coordinates", i)
		}
		for _, coordinates := range coordinates {
			polygon, err := localityGeometry(coordinates)
			if err != nil {
				return nil, fmt.Errorf("localities feature %d: %w", i, err)
			}
			result.polygons = append(result.polygons, localityPolygon{name, polygon, polygon.Bound()})
		}
	}
	return result, nil
}

func localityGeometry(coordinates [][][]float64) (orb.Polygon, error) {
	if len(coordinates) == 0 {
		return nil, fmt.Errorf("empty polygon")
	}
	polygon := make(orb.Polygon, 0, len(coordinates))
	for _, positions := range coordinates {
		if len(positions) < 4 {
			return nil, fmt.Errorf("ring needs at least four positions")
		}
		ring := make(orb.Ring, 0, len(positions))
		for _, position := range positions {
			if len(position) != 2 || math.IsNaN(position[0]) || math.IsNaN(position[1]) || math.Abs(position[0]) > 180 || math.Abs(position[1]) > 90 {
				return nil, fmt.Errorf("invalid WGS84 position")
			}
			ring = append(ring, orb.Point{position[0], position[1]})
		}
		if ring[0] != ring[len(ring)-1] || planar.Area(ring) == 0 {
			return nil, fmt.Errorf("ring must be closed and have nonzero area")
		}
		for i := 1; i < len(ring); i++ {
			if math.Abs(ring[i][0]-ring[i-1][0]) > 180 {
				return nil, fmt.Errorf("antimeridian crossings are unsupported")
			}
		}
		polygon = append(polygon, ring)
	}
	return polygon, nil
}

// Enrich fills only absent locality tags. Conflicting overlaps remain unassigned.
// Polygon holes (including their edges) are excluded; outer edges are included.
func (l *Localities) Enrich(record *pack.Record) {
	if l == nil || strings.TrimSpace(record.Locality) != "" {
		return
	}
	point := orb.Point{record.Longitude, record.Latitude}
	name := ""
	for _, boundary := range l.polygons {
		if !boundary.bound.Contains(point) || !planar.PolygonContains(boundary.polygon, point) {
			continue
		}
		if name != "" && name != boundary.name {
			return
		}
		name = boundary.name
	}
	record.Locality = name
}
