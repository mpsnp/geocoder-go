package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/GameTec-live/geocoder-go/internal/pack"
	"github.com/qedus/osmpbf"
)

func importPBF(ctx context.Context, builder *pack.Builder, path string, options Options) error {
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		return fmt.Errorf("gzip-wrapped PBF is not supported; PBF already uses internal compression")
	}
	if options.Source == "" {
		options.Source = sourceName(path, options)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Compressed PBF size is a useful sizing heuristic for the filter. An
	// undersized filter only stages more unused coordinates; it cannot lose data.
	needed := newNodeFilter(max(1, fileInfo.Size()/24))
	var referenceCount int64
	tx := builder.Tx()
	if tx == nil {
		return fmt.Errorf("pack builder transaction is not active")
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TEMP TABLE osm_ways(id INTEGER PRIMARY KEY, tags TEXT NOT NULL);
CREATE TEMP TABLE osm_way_nodes(way_id INTEGER NOT NULL, node_id INTEGER NOT NULL);
CREATE TEMP TABLE osm_node_coords(id INTEGER PRIMARY KEY, lat REAL NOT NULL, lon REAL NOT NULL);`); err != nil {
		return fmt.Errorf("create PBF staging tables: %w", err)
	}
	wayInsert, err := tx.PrepareContext(ctx, `INSERT INTO osm_ways(id,tags) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = wayInsert.Close() }()
	refInsert, err := tx.PrepareContext(ctx, `INSERT INTO osm_way_nodes(way_id,node_id) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = refInsert.Close() }()

	if err := decodePBF(path, func(value any) error {
		switch object := value.(type) {
		case *osmpbf.Node:
			if !shouldImportOSM(object.Tags, options.IncludeRoads) {
				return nil
			}
			record := osmRecord(object.Tags, object.Lat, object.Lon, "node", object.ID, options)
			return addRecord(ctx, builder, record, options)
		case *osmpbf.Way:
			if !shouldImportOSM(object.Tags, options.IncludeRoads) || len(object.NodeIDs) == 0 {
				return nil
			}
			tags, _ := json.Marshal(object.Tags)
			if _, err := wayInsert.ExecContext(ctx, object.ID, string(tags)); err != nil {
				return err
			}
			for _, nodeID := range object.NodeIDs {
				if _, err := refInsert.ExecContext(ctx, object.ID, nodeID); err != nil {
					return err
				}
				needed.Add(nodeID)
				referenceCount++
			}
		}
		return nil
	}, options); err != nil {
		return fmt.Errorf("first PBF pass: %w", err)
	}

	if err := refInsert.Close(); err != nil {
		return err
	}
	if options.Logf != nil {
		options.Logf("PBF second pass filter contains %d way node references", referenceCount)
	}
	coordInsert, err := tx.PrepareContext(ctx, `INSERT INTO osm_node_coords(id,lat,lon) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	if err := decodePBF(path, func(value any) error {
		node, ok := value.(*osmpbf.Node)
		if !ok {
			return nil
		}
		if !needed.Contains(node.ID) {
			return nil
		}
		_, err := coordInsert.ExecContext(ctx, node.ID, node.Lat, node.Lon)
		return err
	}, options); err != nil {
		_ = coordInsert.Close()
		return fmt.Errorf("second PBF pass: %w", err)
	}
	if err := coordInsert.Close(); err != nil {
		return err
	}
	needed = nil

	centroids, err := tx.QueryContext(ctx, `
SELECT w.id,w.tags,AVG(n.lat),AVG(n.lon)
FROM osm_ways w
JOIN osm_way_nodes wn ON wn.way_id=w.id
JOIN osm_node_coords n ON n.id=wn.node_id
GROUP BY w.id,w.tags`)
	if err != nil {
		return err
	}
	for centroids.Next() {
		var id int64
		var tagsJSON string
		var lat, lon float64
		if err := centroids.Scan(&id, &tagsJSON, &lat, &lon); err != nil {
			_ = centroids.Close()
			return err
		}
		var tags map[string]string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			_ = centroids.Close()
			return err
		}
		if err := addRecord(ctx, builder, osmRecord(tags, lat, lon, "way", id, options), options); err != nil {
			_ = centroids.Close()
			return err
		}
	}
	if err := centroids.Close(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DROP TABLE osm_node_coords; DROP TABLE osm_way_nodes; DROP TABLE osm_ways;`)
	return err
}

const (
	nodeFilterBlockWords = 8 // One cache line per block.
	nodeFilterBitsPerID  = 12
	maxNodeFilterBytes   = 256 << 20
)

// nodeFilter is a bounded-memory Bloom filter. False positives only add unused
// coordinates to the staging table; false negatives, which would affect way
// centroids, are not possible.
type nodeFilter struct {
	words     []uint64
	blockMask uint64
}

func newNodeFilter(items int64) *nodeFilter {
	blocksNeeded := uint64(max(1, items*nodeFilterBitsPerID/(nodeFilterBlockWords*64)))
	maxBlocks := uint64(maxNodeFilterBytes / (nodeFilterBlockWords * 8))
	blocks := uint64(1)
	for blocks < blocksNeeded && blocks < maxBlocks {
		blocks <<= 1
	}
	return &nodeFilter{
		words:     make([]uint64, blocks*nodeFilterBlockWords),
		blockMask: blocks - 1,
	}
}

func (filter *nodeFilter) Add(id int64) {
	filter.update(id, true)
}

func (filter *nodeFilter) Contains(id int64) bool {
	return filter.update(id, false)
}

func (filter *nodeFilter) update(id int64, add bool) bool {
	hash := mixNodeID(uint64(id))
	block := ((hash >> 32) & filter.blockMask) * nodeFilterBlockWords
	positions := [4]uint64{hash, hash >> 13, hash >> 26, hash >> 39}
	found := true
	for _, position := range positions {
		bit := position & 511
		word := block + bit/64
		mask := uint64(1) << (bit & 63)
		if filter.words[word]&mask == 0 {
			found = false
			if add {
				filter.words[word] |= mask
			}
		}
	}
	return found
}

func mixNodeID(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func decodePBF(path string, consume func(any) error, options Options) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := osmpbf.NewDecoder(file)
	if err := decoder.Start(max(1, runtime.GOMAXPROCS(0))); err != nil {
		return err
	}
	var decoded int64
	for {
		value, err := decoder.Decode()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		decoded++
		if decoded%1_000_000 == 0 && options.Logf != nil {
			options.Logf("decoded %d PBF objects", decoded)
		}
		if err := consume(value); err != nil {
			return err
		}
	}
}

func shouldImportOSM(tags map[string]string, includeRoads bool) bool {
	if tags["addr:housenumber"] != "" || tags["addr:street"] != "" {
		return true
	}
	if tags["place"] != "" && tags["name"] != "" {
		return true
	}
	if tags["name"] != "" {
		if tags["landuse"] == "residential" {
			return true
		}
		for _, key := range []string{"amenity", "shop", "tourism", "leisure", "aeroway", "railway", "public_transport", "office", "healthcare", "historic"} {
			if tags[key] != "" {
				return true
			}
		}
	}
	return includeRoads && tags["highway"] != "" && tags["name"] != ""
}

func osmRecord(tags map[string]string, lat, lon float64, objectType string, id int64, options Options) pack.Record {
	kind := "place"
	importance := 0.4
	switch {
	case tags["addr:housenumber"] != "" || tags["addr:street"] != "":
		kind, importance = "address", 1
	case isSettlement(tags["place"]):
		kind, importance = "locality", localityImportance(tags["place"])
	case isNamedPOI(tags):
		kind, importance = "place", 0.4
	case tags["highway"] != "":
		kind, importance = "road", 0.3
	}
	aliases := make([]string, 0, 4)
	for key, value := range tags {
		if (strings.HasPrefix(key, "name:") || key == "alt_name" || key == "short_name" || key == "official_name") && value != "" {
			aliases = append(aliases, value)
		}
	}
	locality := firstTag(tags, "addr:city", "addr:place", "addr:town", "addr:village")
	if locality == "" && kind == "locality" {
		locality = tags["name"]
	}
	localityType := ""
	if kind == "locality" {
		localityType = tags["official_status"]
	}
	return pack.Record{
		LocalityType: localityType,
		Source:       options.Source, SourceID: "osm:" + objectType + ":" + strconv.FormatInt(id, 10), Kind: kind,
		Name: tags["name"], HouseNumber: tags["addr:housenumber"], Street: firstTag(tags, "addr:street", "addr:place"), Unit: tags["addr:unit"],
		Postcode: tags["addr:postcode"], Locality: locality, District: firstTag(tags, "addr:district", "addr:suburb"),
		Region: tags["addr:state"], CountryCode: firstTag(tags, "addr:country", "ISO3166-1:alpha2"),
		Latitude: lat, Longitude: lon, Importance: importance, Aliases: aliases,
	}
}

func isNamedPOI(tags map[string]string) bool {
	if tags["name"] == "" {
		return false
	}
	for _, key := range []string{"amenity", "shop", "tourism", "leisure", "aeroway", "railway", "public_transport", "office", "healthcare", "historic"} {
		if tags[key] != "" {
			return true
		}
	}
	return false
}

func firstTag(tags map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := tags[key]; value != "" {
			return value
		}
	}
	return ""
}

func localityImportance(placeType string) float64 {
	switch placeType {
	case "city":
		return 1
	case "town":
		return 0.8
	case "village":
		return 0.6
	default:
		return 0.4
	}
}

// Suburbs and neighbourhoods are places within a settlement, not cities themselves.
func isSettlement(place string) bool {
	switch place {
	case "city", "town", "village", "hamlet", "isolated_dwelling":
		return true
	default:
		return false
	}
}
