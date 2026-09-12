package geocoder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/GameTec-live/geocoder-go/internal/pack"
	_ "modernc.org/sqlite"
)

// Unicode White_Space, matching strings.TrimSpace for address eligibility.
const addressWhitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

var packExtensions = map[string]bool{".db": true, ".sqlite": true, ".sqlite3": true}

type packHandle struct {
	Name            string
	localityTypeSQL string
	Path            string
	DB              *sql.DB
}

type Service struct {
	root  string
	packs []*packHandle
	once  sync.Once
}

type SearchOptions struct {
	// HouseAddressesOnly excludes incomplete addresses, POIs and road fallback.
	HouseAddressesOnly bool
	Query              string
	CountryCode        string
	Latitude           *float64
	Longitude          *float64
	Limit              int
}

type ReverseOptions struct {
	Latitude    float64
	Longitude   float64
	RadiusMeter float64
	Limit       int
}

type Result struct {
	Pack          string   `json:"pack"`
	Source        string   `json:"source,omitempty"`
	SourceID      string   `json:"source_id,omitempty"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name,omitempty"`
	HouseNumber   string   `json:"house_number,omitempty"`
	Street        string   `json:"street,omitempty"`
	Unit          string   `json:"unit,omitempty"`
	Postcode      string   `json:"postcode,omitempty"`
	LocalityType  string   `json:"locality_type,omitempty"`
	Locality      string   `json:"locality,omitempty"`
	District      string   `json:"district,omitempty"`
	Region        string   `json:"region,omitempty"`
	CountryCode   string   `json:"country_code,omitempty"`
	Country       string   `json:"country,omitempty"`
	Latitude      float64  `json:"lat"`
	Longitude     float64  `json:"lon"`
	Importance    float64  `json:"importance,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
	DisplayName   string   `json:"display_name"`
	Score         float64  `json:"score,omitempty"`
	DistanceMeter float64  `json:"distance_m,omitempty"`

	rank       float64
	searchText string
}

func Open(ctx context.Context, root string) (*Service, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	service := &Service{root: abs}
	err = filepath.WalkDir(abs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !packExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}
		opened, openErr := openPack(ctx, abs, path)
		if openErr != nil {
			return fmt.Errorf("open pack %s: %w", path, openErr)
		}
		service.packs = append(service.packs, opened)
		return nil
	})
	if err != nil {
		_ = service.Close()
		return nil, err
	}
	sort.Slice(service.packs, func(i, j int) bool { return service.packs[i].Name < service.packs[j].Name })
	return service, nil
}

func openPack(ctx context.Context, root, path string) (*packHandle, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(abs) + "?mode=ro&immutable=1&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var version string
	if err := db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='schema_version'`).Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("not an AtlasTaxi geocoder pack: %w", err)
	}
	if version != strconv.Itoa(pack.SchemaVersion) {
		_ = db.Close()
		return nil, fmt.Errorf("unsupported schema version %q", version)
	}
	if err := db.QueryRowContext(ctx, `SELECT 1 FROM records_fts LIMIT 1`).Scan(new(int)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("missing search index: %w", err)
	}
	hasType, err := pack.HasLocalityType(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	typeSQL := "''"
	if hasType {
		typeSQL = "r.locality_type"
	}
	rel, _ := filepath.Rel(root, abs)
	return &packHandle{Name: filepath.ToSlash(rel), Path: abs, DB: db, localityTypeSQL: typeSQL}, nil
}

func (s *Service) PackNames() []string {
	result := make([]string, len(s.packs))
	for i, p := range s.packs {
		result[i] = p.Name
	}
	return result
}

func (s *Service) Geocode(ctx context.Context, options SearchOptions) ([]Result, error) {
	normalized := pack.Normalize(options.Query)
	if normalized == "" {
		return nil, errors.New("query is empty")
	}
	options.CountryCode = strings.ToUpper(strings.TrimSpace(options.CountryCode))
	options.Limit = clamp(options.Limit, 1, 50, 10)
	match := ftsQuery(normalized)
	// FTS considers every token a prefix, so a query for house number "1" also
	// retrieves 10, 101, and 152. Keep enough candidates for field-aware scoring
	// to promote the exact house number rather than trusting FTS rank alone.
	perPack := max(options.Limit*20, 100)
	var results []Result
	for _, current := range s.packs {
		rows, err := current.DB.QueryContext(ctx, strings.ReplaceAll(`
SELECT r.source,r.source_id,r.kind,r.name,r.house_number,r.street,r.unit,r.postcode,
       r.locality,LOCALITY_TYPE,r.district,r.region,r.country_code,r.country,r.lat,r.lon,r.importance,
       r.aliases,r.display_name,r.search_text,bm25(records_fts)
FROM records_fts
JOIN records r ON r.id=records_fts.rowid
WHERE records_fts MATCH ? AND (?='' OR r.country_code=?)
  AND (NOT ? OR (r.kind='address' AND trim(r.street,?)<>'' AND trim(r.house_number,?)<>''))
ORDER BY bm25(records_fts)
LIMIT ?`, "LOCALITY_TYPE", current.localityTypeSQL), match, options.CountryCode, options.CountryCode, options.HouseAddressesOnly, addressWhitespace, addressWhitespace, perPack)
		if err != nil {
			return nil, fmt.Errorf("query pack %s: %w", current.Name, err)
		}
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			result, err := scanResult(rows, current.Name)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			if options.Latitude != nil && options.Longitude != nil {
				result.DistanceMeter = haversine(*options.Latitude, *options.Longitude, result.Latitude, result.Longitude)
			}
			result.Score = searchScore(result, normalized, options.Latitude, options.Longitude)
			results = append(results, result)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	// FTS prefix matching is intentionally the fast path. If it cannot find a
	// result, retrieve a bounded set using two-character token prefixes and
	// validate those candidates with edit distance. This catches ordinary
	// destination-entry mistakes without turning every successful query into a
	// much broader scan.
	if len(results) == 0 && fuzzyEligible(normalized) {
		perPack = max(options.Limit*100, 1000)
		variants, err := fuzzyQueryVariants(ctx, normalized)
		if err != nil {
			return nil, err
		}
		for _, fuzzyQuery := range variants {
			match = fuzzyFTSQuery(fuzzyQuery)
			foundVariant := false
			for _, current := range s.packs {
				rows, err := current.DB.QueryContext(ctx, strings.ReplaceAll(`
SELECT r.source,r.source_id,r.kind,r.name,r.house_number,r.street,r.unit,r.postcode,
       r.locality,LOCALITY_TYPE,r.district,r.region,r.country_code,r.country,r.lat,r.lon,r.importance,
       r.aliases,r.display_name,r.search_text,bm25(records_fts)
FROM records_fts
JOIN records r ON r.id=records_fts.rowid
WHERE records_fts MATCH ? AND (?='' OR r.country_code=?)
  AND (NOT ? OR (r.kind='address' AND trim(r.street,?)<>'' AND trim(r.house_number,?)<>''))
ORDER BY bm25(records_fts)
LIMIT ?`, "LOCALITY_TYPE", current.localityTypeSQL), match, options.CountryCode, options.CountryCode, options.HouseAddressesOnly, addressWhitespace, addressWhitespace, perPack)
				if err != nil {
					return nil, fmt.Errorf("fuzzy query pack %s: %w", current.Name, err)
				}
				for rows.Next() {
					if err := ctx.Err(); err != nil {
						_ = rows.Close()
						return nil, err
					}
					result, err := scanResult(rows, current.Name)
					if err != nil {
						_ = rows.Close()
						return nil, err
					}
					score, ok := fuzzySearchScore(ctx, result, fuzzyQuery)
					if err := ctx.Err(); err != nil {
						_ = rows.Close()
						return nil, err
					}
					if !ok {
						continue
					}
					if options.Latitude != nil && options.Longitude != nil {
						result.DistanceMeter = haversine(*options.Latitude, *options.Longitude, result.Latitude, result.Longitude)
						score -= math.Min(result.DistanceMeter/10000, 20)
					}
					result.Score = score
					results = append(results, result)
					foundVariant = true
				}
				if err := rows.Err(); err != nil {
					_ = rows.Close()
					return nil, err
				}
				if err := rows.Close(); err != nil {
					return nil, err
				}
			}
			if foundVariant {
				break
			}
		}
	}
	if len(results) == 0 && !options.HouseAddressesOnly {
		roadResults, err := s.geocodeRoad(ctx, options)
		if err != nil {
			return nil, err
		}
		results = append(results, roadResults...)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	results = deduplicate(results, options.Limit)
	return results, nil
}

type roadQuery struct {
	road     string
	locality string
}

// geocodeRoad resolves the locality independently from a named highway way.
// OSM highway ways usually have a name but no addr:city tag; Nominatim derives
// that context from administrative polygons. Here we use the much smaller set
// of indexed place nodes and select the matching road segment nearest to the
// requested locality. The distance ceiling prevents a unique-looking name on
// the other side of the country from being accepted silently.
func (s *Service) geocodeRoad(ctx context.Context, options SearchOptions) ([]Result, error) {
	var results []Result
	for _, query := range roadQueries(options.Query) {
		localities, err := s.queryKind(ctx, query.locality, options.CountryCode, "locality", 30)
		if err != nil {
			return nil, err
		}
		roads, err := s.queryKind(ctx, query.road, options.CountryCode, "road", 500)
		if err != nil {
			return nil, err
		}
		wantedRoad := compact(query.road)
		wantedLocality := pack.Normalize(query.locality)
		best := make(map[string]Result)
		for _, locality := range localities {
			localityName := firstNonEmpty(locality.Name, locality.Locality)
			if pack.Normalize(localityName) != wantedLocality {
				continue
			}
			for _, road := range roads {
				if compact(firstNonEmpty(road.Name, road.Street)) != wantedRoad {
					continue
				}
				distance := haversine(locality.Latitude, locality.Longitude, road.Latitude, road.Longitude)
				if distance > roadLocalityRadius(locality.Importance) {
					continue
				}
				road.Locality = localityName
				road.LocalityType = locality.LocalityType
				if road.District == "" {
					road.District = locality.District
				}
				if road.Region == "" {
					road.Region = locality.Region
				}
				if road.Postcode == "" {
					road.Postcode = locality.Postcode
				}
				road.DisplayName = strings.TrimSpace(firstNonEmpty(road.Name, road.Street) + ", " + localityName)
				road.Score = 100 + locality.Importance*10 - distance/5000
				key := compact(road.DisplayName)
				if previous, exists := best[key]; !exists || road.Score > previous.Score {
					best[key] = road
				}
			}
		}
		for _, result := range best {
			results = append(results, result)
		}
		if len(results) > 0 {
			break
		}
	}
	return results, nil
}

func roadLocalityRadius(importance float64) float64 {
	switch {
	case importance >= 1: // city
		return 25_000
	case importance >= 0.8: // town
		return 15_000
	case importance >= 0.6: // village
		return 8_000
	default: // suburb, hamlet, or other place node
		return 5_000
	}
}

func (s *Service) queryKind(ctx context.Context, query, countryCode, kind string, limit int) ([]Result, error) {
	normalized := pack.Normalize(query)
	if normalized == "" {
		return nil, nil
	}
	var results []Result
	for _, current := range s.packs {
		rows, err := current.DB.QueryContext(ctx, strings.ReplaceAll(`
SELECT r.source,r.source_id,r.kind,r.name,r.house_number,r.street,r.unit,r.postcode,
       r.locality,LOCALITY_TYPE,r.district,r.region,r.country_code,r.country,r.lat,r.lon,r.importance,
       r.aliases,r.display_name,r.search_text,bm25(records_fts)
FROM records_fts
JOIN records r ON r.id=records_fts.rowid
WHERE records_fts MATCH ? AND r.kind=? AND (?='' OR r.country_code=?)
ORDER BY bm25(records_fts)
LIMIT ?`, "LOCALITY_TYPE", current.localityTypeSQL), ftsQuery(normalized), kind, countryCode, countryCode, limit)
		if err != nil {
			return nil, fmt.Errorf("query %s records in pack %s: %w", kind, current.Name, err)
		}
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			result, err := scanResult(rows, current.Name)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			results = append(results, result)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func roadQueries(query string) []roadQuery {
	var result []roadQuery
	commaParts := strings.Split(query, ",")
	if len(commaParts) > 1 {
		road := stripHouseNumber(strings.TrimSpace(commaParts[0]))
		locality := withoutNumericTokens(strings.Join(commaParts[1:], " "))
		if road != "" && locality != "" {
			result = append(result, roadQuery{road: road, locality: locality})
		}
		return result
	}
	tokens := strings.Fields(query)
	for split := len(tokens) - 1; split > 0; split-- {
		road := stripHouseNumber(strings.Join(tokens[:split], " "))
		locality := withoutNumericTokens(strings.Join(tokens[split:], " "))
		if road != "" && locality != "" {
			result = append(result, roadQuery{road: road, locality: locality})
		}
	}
	return result
}

func stripHouseNumber(value string) string {
	tokens := strings.Fields(value)
	if len(tokens) < 2 {
		return strings.TrimSpace(value)
	}
	last := tokens[len(tokens)-1]
	if len(last) <= 8 && strings.ContainsAny(last, "0123456789") {
		tokens = tokens[:len(tokens)-1]
	}
	return strings.Join(tokens, " ")
}

func withoutNumericTokens(value string) string {
	var tokens []string
	for _, token := range strings.Fields(value) {
		if strings.ContainsAny(token, "0123456789") {
			continue
		}
		tokens = append(tokens, token)
	}
	return strings.Join(tokens, " ")
}

func compact(value string) string {
	return strings.ReplaceAll(pack.Normalize(value), " ", "")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (s *Service) Reverse(ctx context.Context, options ReverseOptions) ([]Result, error) {
	if options.Latitude < -90 || options.Latitude > 90 || options.Longitude < -180 || options.Longitude > 180 {
		return nil, errors.New("coordinates are outside WGS84 bounds")
	}
	if options.RadiusMeter <= 0 {
		options.RadiusMeter = 5000
	}
	if options.RadiusMeter > 100000 {
		return nil, errors.New("radius_m must not exceed 100000")
	}
	options.Limit = clamp(options.Limit, 1, 50, 1)
	latDelta := options.RadiusMeter / 111320.0
	lonScale := math.Cos(options.Latitude * math.Pi / 180)
	if math.Abs(lonScale) < 0.01 {
		lonScale = 0.01
	}
	lonDelta := options.RadiusMeter / (111320.0 * math.Abs(lonScale))
	var results []Result
	for _, current := range s.packs {
		rows, err := current.DB.QueryContext(ctx, strings.ReplaceAll(`
SELECT r.source,r.source_id,r.kind,r.name,r.house_number,r.street,r.unit,r.postcode,
       r.locality,LOCALITY_TYPE,r.district,r.region,r.country_code,r.country,r.lat,r.lon,r.importance,
       r.aliases,r.display_name,r.search_text,0.0
FROM records_rtree x
JOIN records r ON r.id=x.id
WHERE x.min_lat<=? AND x.max_lat>=? AND x.min_lon<=? AND x.max_lon>=?
ORDER BY ((r.lat-?)*(r.lat-?)) + ((r.lon-?)*(r.lon-?)*?)
LIMIT 2000`, "LOCALITY_TYPE", current.localityTypeSQL), options.Latitude+latDelta, options.Latitude-latDelta, options.Longitude+lonDelta, options.Longitude-lonDelta,
			options.Latitude, options.Latitude, options.Longitude, options.Longitude, lonScale*lonScale)
		if err != nil {
			return nil, fmt.Errorf("reverse query pack %s: %w", current.Name, err)
		}
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			result, err := scanResult(rows, current.Name)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			result.DistanceMeter = haversine(options.Latitude, options.Longitude, result.Latitude, result.Longitude)
			if result.DistanceMeter <= options.RadiusMeter {
				result.Score = 1 / (1 + result.DistanceMeter)
				results = append(results, result)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].DistanceMeter == results[j].DistanceMeter {
			return results[i].Importance > results[j].Importance
		}
		return results[i].DistanceMeter < results[j].DistanceMeter
	})
	return deduplicate(results, options.Limit), nil
}

type scanner interface{ Scan(...any) error }

func scanResult(row scanner, packName string) (Result, error) {
	var result Result
	var aliases string
	err := row.Scan(&result.Source, &result.SourceID, &result.Kind, &result.Name, &result.HouseNumber,
		&result.Street, &result.Unit, &result.Postcode, &result.Locality, &result.LocalityType, &result.District,
		&result.Region, &result.CountryCode, &result.Country, &result.Latitude, &result.Longitude,
		&result.Importance, &aliases, &result.DisplayName, &result.searchText, &result.rank)
	if err != nil {
		return Result{}, err
	}
	result.Pack = packName
	_ = json.Unmarshal([]byte(aliases), &result.Aliases)
	return result, nil
}

func ftsQuery(query string) string {
	tokens := strings.Fields(query)
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		parts = append(parts, `"`+strings.ReplaceAll(token, `"`, `""`)+`"*`)
	}
	return strings.Join(parts, " AND ")
}

func fuzzyEligible(query string) bool {
	for _, token := range strings.Fields(query) {
		if len([]rune(token)) >= 5 {
			return true
		}
	}
	return false
}

func fuzzyFTSQuery(query string) string {
	tokens := strings.Fields(query)
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		runes := []rune(token)
		if len(runes) > 2 {
			token = string(runes[:2])
		}
		parts = append(parts, `"`+strings.ReplaceAll(token, `"`, `""`)+`"*`)
	}
	return strings.Join(parts, " AND ")
}

func fuzzyQueryVariants(ctx context.Context, query string) ([]string, error) {
	tokens := strings.Fields(query)
	variants := []string{query}
	seen := map[string]struct{}{query: {}}
	level := [][]string{tokens}
	// Remove at most two word boundaries. The boundaries may be adjacent
	// ("Str as se") or independent ("Maria hilfer Str asse").
	for depth := 0; depth < 2; depth++ {
		var next [][]string
		for _, current := range level {
			for boundary := 0; boundary+1 < len(current); boundary++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				merged := make([]string, 0, len(current)-1)
				merged = append(merged, current[:boundary]...)
				merged = append(merged, current[boundary]+current[boundary+1])
				merged = append(merged, current[boundary+2:]...)
				variant := strings.Join(merged, " ")
				if _, exists := seen[variant]; exists {
					continue
				}
				seen[variant] = struct{}{}
				variants = append(variants, variant)
				next = append(next, merged)
			}
		}
		level = next
	}
	return variants, nil
}

func fuzzySearchScore(ctx context.Context, result Result, query string) (float64, bool) {
	queryTokens := strings.Fields(query)
	candidateTokens := tokenVariants(ctx, strings.Fields(result.searchText))
	if len(queryTokens) == 0 || len(candidateTokens) == 0 {
		return 0, false
	}
	totalSimilarity := 0.0
	for _, queryToken := range queryTokens {
		best := len([]rune(queryToken)) + 1
		for _, candidateToken := range candidateTokens {
			distance := levenshtein(ctx, queryToken, candidateToken)
			if distance < 0 {
				return 0, false
			}
			if distance < best {
				best = distance
			}
		}
		allowed := allowedEdits(queryToken)
		if best > allowed {
			return 0, false
		}
		length := max(len([]rune(queryToken)), 1)
		totalSimilarity += 1 - float64(best)/float64(length)
	}
	averageSimilarity := totalSimilarity / float64(len(queryTokens))
	return 40 + result.Importance*10 + averageSimilarity*40 - result.rank, true
}

func tokenVariants(ctx context.Context, tokens []string) []string {
	variants := append([]string(nil), tokens...)
	for i := range tokens {
		if ctx.Err() != nil {
			return nil
		}
		combined := tokens[i]
		for j := i + 1; j < len(tokens) && j <= i+2; j++ {
			combined += tokens[j]
			variants = append(variants, combined)
		}
	}
	return variants
}

func allowedEdits(token string) int {
	runes := []rune(token)
	for _, r := range runes {
		if r >= '0' && r <= '9' {
			return 0
		}
	}
	switch {
	case len(runes) >= 9:
		return 2
	case len(runes) >= 5:
		return 1
	default:
		return 0
	}
}

func levenshtein(ctx context.Context, a, b string) int {
	left, right := []rune(a), []rune(b)
	if len(left) > len(right) {
		left, right = right, left
	}
	previous := make([]int, len(left)+1)
	for i := range previous {
		previous[i] = i
	}
	for row, rightRune := range right {
		if ctx.Err() != nil {
			return -1
		}
		current := make([]int, len(left)+1)
		current[0] = row + 1
		for column, leftRune := range left {
			if column%256 == 0 && ctx.Err() != nil {
				return -1
			}
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[column+1] = min(current[column]+1, previous[column+1]+1, previous[column]+cost)
		}
		previous = current
	}
	return previous[len(left)]
}

func searchScore(result Result, query string, biasLat, biasLon *float64) float64 {
	score := 50.0 + result.Importance*10 - result.rank
	if result.searchText == query {
		score += 50
	} else if strings.HasPrefix(result.searchText, query) {
		score += 20
	} else if strings.Contains(result.searchText, query) {
		score += 10
	}
	queryTokens := make(map[string]struct{})
	for _, token := range strings.Fields(query) {
		queryTokens[token] = struct{}{}
	}
	street := pack.Normalize(result.Street)
	if street != "" && (strings.Contains(query, street) || strings.Contains(strings.ReplaceAll(query, " ", ""), strings.ReplaceAll(street, " ", ""))) {
		score += 25
	}
	for value, boost := range map[string]float64{
		pack.Normalize(result.HouseNumber): 30,
		pack.Normalize(result.Postcode):    15,
		pack.Normalize(result.Locality):    12,
	} {
		if value == "" {
			continue
		}
		if _, exact := queryTokens[value]; exact {
			score += boost
		}
	}
	if biasLat != nil && biasLon != nil {
		score -= math.Min(result.DistanceMeter/10000, 20)
	}
	return score
}

func deduplicate(results []Result, limit int) []Result {
	seen := make(map[string]struct{}, len(results))
	out := make([]Result, 0, min(limit, len(results)))
	for _, result := range results {
		key := strings.Join([]string{pack.Normalize(result.DisplayName), fmt.Sprintf("%.5f", result.Latitude), fmt.Sprintf("%.5f", result.Longitude)}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, result)
		if len(out) == limit {
			break
		}
	}
	return out
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371008.8
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return earthRadius * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func clamp(value, low, high, defaultValue int) int {
	if value == 0 {
		return defaultValue
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (s *Service) Close() error {
	var errs []error
	s.once.Do(func() {
		for _, current := range s.packs {
			if err := current.DB.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	})
	return errors.Join(errs...)
}
