# geocoder-go

`geocoder-go` is a low-memory, offline forward and reverse geocoder designed
for embedded and edge deployments. It converts source data into immutable
SQLite packs off-device and serves those packs through a small Gin HTTP API.

The runtime has no PostgreSQL, CGO, Spatialite, DuckDB, or network dependency.
The published container contains one statically linked binary in a rootless
`scratch` image; data packs are mounted separately.

## Features

- Forward geocoding with Unicode normalization, prefix search, German spelling
  variants, bounded typo recovery, and optional coordinate bias.
- Reverse geocoding using an SQLite R-tree and exact Haversine distances.
- Named-road lookup with locality-aware disambiguation for OSM ways that lack
  `addr:city` tags.
- Recursive discovery of any number of `.db`, `.sqlite`, and `.sqlite3` packs.
- Pack generation from CSV/TSV, GeoJSON, NDJSON, OSM PBF, existing packs, and
  Overture address GeoParquet.
- Pure-Go SQLite runtime with one read-only connection per pack.
- Graceful shutdown, liveness/readiness endpoints, and a built-in container
  healthcheck command.
- Multi-architecture `linux/amd64` and `linux/arm64` container publishing.

## Quick start

Create a small example pack and run the server:

```sh
go run ./cmd/packgen build \
  --country AT \
  --source example \
  --output data/example.sqlite \
  examples/addresses.csv

go run ./cmd/server --data data --listen :8080
```

Query it:

```sh
curl "http://localhost:8080/geocode?q=Stephansplatz%201%2C%20Wien&country=AT"
curl "http://localhost:8080/reverse?lat=48.2085&lon=16.3721&radius_m=500"
curl "http://localhost:8080/readyz"
```

## Container

The image does not contain geographic data. Mount one or more generated packs
read-only below `/data`:

```sh
docker run --rm \
  --read-only \
  --user 65532:65532 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  -p 8080:8080 \
  -v "$(pwd)/data:/data:ro" \
  ghcr.io/gametec-live/geocoder-go:latest
```

The included `compose.yaml` applies the same restrictions:

```sh
docker compose up -d
```

Pack files must be readable by UID/GID `65532`, or at least world-readable.

## Documentation

- [HTTP API](docs/API.md)
- [Building and managing packs](docs/PACKS.md)
- [Production operations](docs/OPERATIONS.md)
- [Security policy](SECURITY.md)

## Building locally

Go 1.26 or later is required.

```sh
go mod download
go test ./...
go vet ./...
go build -trimpath -o bin/geocoder ./cmd/server
go build -trimpath -o bin/packgen ./cmd/packgen
```

Runtime build information is available without opening a pack:

```sh
./bin/geocoder version
```

## Architecture

```text
source data                     build machine
CSV / GeoJSON / PBF / Parquet ──> packgen ──> immutable SQLite packs
                                                    │
                                                    │ read-only mount
                                                    ▼
client ──HTTP──> Gin API ──> FTS5 / R-tree ──> ranked JSON results
```

Forward search uses an FTS5 prefix index. Reverse search first uses an R-tree
bounding box and then calculates exact distances for nearby candidates. Packs
are opened with SQLite `mode=ro`, `immutable=1`, and `query_only=1`.

## Scope and limitations

This service intentionally stores a much smaller model than Nominatim or others:

- OSM relations and administrative polygons are not imported.
- Polygon and way features are represented by centroids.
- Reverse lookup returns the nearest indexed point, not a containing boundary.
- House-number interpolation and nearest-point-on-road calculations are not
  implemented.
- A road result for a missing house number is a representative road segment,
  not a claimed building entrance.
- Source data can be incomplete. The geocoder does not invent POI/address
  relationships absent from upstream data.

Applications using results as route destinations should retain `kind`,
`source`, and `source_id`, distinguish exact addresses from road fallbacks, and
allow users to confirm ambiguous destinations.

## Data licensing

The software does not change upstream data licenses. OSM-derived packs remain
subject to the ODbL and its attribution requirements. Overture, OpenAddresses,
INSPIRE, national datasets, and business data may carry additional obligations.
The `source` field is preserved in every result to support attribution.

geocoder-go itself is licensed under the MIT License and free for anyone to use.
