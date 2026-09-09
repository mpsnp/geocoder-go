# HTTP API

The API returns JSON and is intended to sit behind an application gateway or
reverse proxy in production. It does not provide TLS, authentication, CORS, or
rate limiting itself.

## Conventions

- Coordinates use WGS84 decimal degrees.
- `lat` is latitude and `lon` is longitude.
- Limits are clamped to the documented range.
- Successful search responses contain `count` and `results`.
- Error responses use a stable envelope:

```json
{
  "error": {
    "code": "missing_query",
    "message": "q is required"
  }
}
```

All responses include `Cache-Control: no-store` and
`X-Content-Type-Options: nosniff`.

## `GET /geocode`

Forward-geocodes an address, locality, POI, or named road.

| Parameter | Required | Description |
|---|---:|---|
| `q` | yes | Free-text query, maximum 512 Unicode characters. |
| `limit` | no | Result limit, default `10`, range `1`–`50`. |
| `country` | no | ISO 3166-1 alpha-2 country filter, for example `AT`. |
| `lat` | no | Latitude for result bias; must be supplied with `lon`. |
| `lon` | no | Longitude for result bias; must be supplied with `lat`. |

Example:

```http
GET /geocode?q=Altenhofgasse%2C%20Freistadt&country=AT&limit=5
```

```json
{
  "query": "Altenhofgasse, Freistadt",
  "count": 1,
  "results": [
    {
      "pack": "austria.sqlite",
      "source": "openstreetmap",
      "source_id": "osm:way:30359891",
      "kind": "road",
      "name": "Altenhofgasse",
      "locality": "Freistadt",
      "country_code": "AT",
      "lat": 48.51211444,
      "lon": 14.5036545,
      "display_name": "Altenhofgasse, Freistadt",
      "score": 107.9
    }
  ]
}
```

### Matching behavior

The fast path is normalized FTS5 prefix matching. Normalization is
case-insensitive, removes punctuation and diacritics, and expands common German
forms such as `ä/ae` and `ß/ss`. Russian tokens `жк`, `ул`, and `просп` expand
to `жилой комплекс`, `улица`, and `проспект` in both records and queries;
display labels are unchanged.

If strict matching returns nothing, a bounded edit-distance fallback tolerates
small mistakes and accidental word boundaries. Exact house numbers, postcodes,
streets, and localities receive ranking boosts.

If a combined road/locality query still has no result, the service resolves
the locality separately and selects the nearest exact-named road segment within
an adaptive safety radius. Results outside that radius are rejected. A supplied
house number may be omitted from a `kind=road` response when no exact upstream
address exists.

## `GET /reverse`

Returns indexed points near a coordinate, ordered by exact distance.

| Parameter | Required | Description |
|---|---:|---|
| `lat` | yes | Latitude from `-90` to `90`. |
| `lon` | yes | Longitude from `-180` to `180`. |
| `radius_m` | no | Search radius in metres, default `5000`, maximum `100000`. |
| `limit` | no | Result limit, default `1`, range `1`–`50`. |

Example:

```http
GET /reverse?lat=48.20849&lon=16.37208&radius_m=500&limit=1
```

Each result includes `distance_m`. Reverse lookup considers indexed point
locations; it does not perform polygon containment or project onto line
geometry.

## Health endpoints

### `GET /healthz`

Liveness check. A `200` response means the process and HTTP router are running.

```json
{"status":"ok"}
```

### `GET /readyz`

Readiness and pack inventory. Startup fails if no valid pack is found, so a
running server normally reports at least one pack.

```json
{
  "status": "ready",
  "pack_count": 1,
  "packs": ["austria.sqlite"]
}
```

## Result fields

Fields with empty values are omitted where the JSON schema uses `omitempty`.

| Field | Meaning |
|---|---|
| `pack` | Relative path of the pack that produced the result. |
| `source` | Upstream dataset attribution. |
| `source_id` | Stable upstream object identifier when available. |
| `kind` | `address`, `locality`, `place`, or `road`. |
| `name` | Feature or POI name. |
| `house_number`, `street`, `unit` | Address components. |
| `postcode`, `locality`, `district`, `region` | Administrative/address context. |
| `country_code`, `country` | Country context. |
| `lat`, `lon` | WGS84 result point. |
| `display_name` | Human-readable label. |
| `score` | Engine-specific relevance score; compare only within one response. |
| `distance_m` | Distance from supplied coordinates when applicable. |

## Status codes

| Status | Meaning |
|---:|---|
| `200` | Request completed, including valid searches with zero results. |
| `400` | Missing or invalid parameters. |
| `404` | Unknown endpoint. |
| `500` | Pack query or internal search failure. |
