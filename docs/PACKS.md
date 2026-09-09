# Building and managing packs

Pack generation is an offline build operation. Do not run `packgen` on a
memory-constrained production device unless the input is known to be small.

## Pack discovery

At startup, the server recursively scans `--data`/`GEOCODER_DATA`. Files ending
in `.db`, `.sqlite`, or `.sqlite3` are treated as packs; directory layout and
file names are otherwise unrestricted.

```text
data/
  austria.sqlite
  eu/cities.sqlite
  overrides/verified-pois.db
```

Every discovered file must have the expected schema version and FTS index.
Startup fails on an invalid pack. Packs are opened read-only and immutable and
are not reloaded until the server restarts.

## Commands

```text
packgen build [flags] <input> [input...]
packgen overture --output <pack> --country <CC> --release <release>
packgen inspect <pack>
```

Common build flags:

| Flag | Description |
|---|---|
| `--output` | Required output pack. |
| `--country` | Fallback ISO country code and Overture filter. |
| `--source` | Attribution stored with imported records. |
| `--format` | `auto`, `csv`, `tsv`, `geojson`, `ndjson`, `pbf`, `sqlite`, or `parquet`. |
| `--append` | Add to an existing pack and rebuild its indexes. |
| `--strict` | Stop on the first malformed record instead of logging and skipping it. |
| `--localities` | Optional GeoJSON settlement boundaries for missing locality tags. |
| `--include-roads` | Include named OSM highway ways; defaults to true. |

Use `--include-roads=false` for a smaller address/POI-only pack.

`--localities boundaries.geojson` fills missing localities by containment of
record points. Supply a nonempty WGS84 `FeatureCollection` of valid settlement
`Polygon`/`MultiPolygon` features with `properties.locality` names and closed
2D rings. Holes are excluded; conflicting overlaps remain unassigned. Existing
localities are preserved. Antimeridian crossings are unsupported. Input is
validated before opening the output pack, and its SHA256 is stored as
`locality_boundaries_sha256` for that import.

Rebuild from source to apply normalization or OSM classification changes to
existing packs. With `--append`, only incoming records receive enrichment.

## OSM PBF

```sh
packgen build \
  --country AT \
  --source openstreetmap \
  --output data/austria.sqlite.building \
  austria-latest.osm.pbf

packgen inspect data/austria.sqlite.building
```

The importer makes two PBF passes. It imports tagged addresses, named
settlements, neighbourhoods, named residential land-use areas, selected named
POIs, and named roads. Settlements use `kind=locality`; neighbourhoods and
residential areas use `kind=place`. Selected ways are represented by their
node-average centroid. OSM relations are currently ignored; settlement
boundaries for `--localities` must be supplied separately.

PBF staging tables and index builds use temporary disk space. The second pass
uses an in-memory filter capped at 256 MiB to select coordinates for way
centroids, so RAM use does not grow without limit on large extracts. Ensure the
build machine has enough free temporary-disk space in addition to space for the
output pack.

Use country or regional extracts rather than a continental PBF when possible.

## CSV and TSV

At minimum, each row needs `lat`, `lon`, and a searchable name or address
component. Recognized columns include:

```text
source, source_id, kind, name, house_number, street, unit, postcode,
locality, district, region, country_code, country, lat, lon,
importance, aliases, display_name
```

Common aliases such as `latitude`, `longitude`, `city`, `state`, `zip`,
`housenumber`, and `addr:street` are accepted. Separate aliases with `|` or
`;`. CSV/TSV input may be gzip-compressed.

## GeoJSON and NDJSON

FeatureCollections are streamed. NDJSON accepts one feature per line. Point
coordinates are used directly; polygon-like geometries are reduced to a
representative centroid.

Supported suffixes include `.geojson`, `.json`, `.ndjson`, `.geojsonl`, and
`.jsonl`, optionally followed by `.gz`.

## Existing packs

An existing pack can be supplied to `packgen build` alongside other inputs.
Records are copied through the common normalization and fingerprinting path,
which enables country, regional, or override packs to be assembled without
changing the runtime.

## Overture addresses

The Overture adapter requires the DuckDB CLI on the build machine. DuckDB is
used only to stream matching address rows from GeoParquet into the pack builder.

```sh
packgen overture \
  --release 2026-06-17.0 \
  --country AT \
  --output data/overture-at.sqlite
```

DuckDB needs network access to load its `spatial` and `httpfs` extensions when
using the official S3 release. Use `--input` for a local or alternate
GeoParquet glob.

The current adapter targets the Overture address schema, not the Places theme.

## Atomic rollout

Never modify a pack while the server has it open. Build to a temporary suffix
that the scanner ignores, validate it, stop or drain the server, then rename it:

```sh
packgen inspect data/austria.sqlite.building
mv data/austria.sqlite data/austria.sqlite.previous
mv data/austria.sqlite.building data/austria.sqlite
```

Restart the server and verify `/readyz`, representative forward searches, and
reverse searches before deleting the previous file. On Windows, stop the
server before renaming an open pack.

## Pack internals

Each schema-versioned pack contains:

- A normalized `records` table.
- An external-content FTS5 index with two-, three-, and four-character prefixes.
- An R-tree point index for reverse lookup.
- Source attribution and build metadata.
- Fingerprints for deterministic deduplication.

Finalized packs checkpoint and truncate their write-ahead log. A deployed pack
must not have required `-wal` or `-shm` sidecars.

## Licensing and provenance

Record the real upstream source with `--source`; do not label third-party data
as OSM merely because it is merged into an OSM-derived pack. Keep source
versions, download dates, build commands, licenses, and required attribution in
deployment records.
