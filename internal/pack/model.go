package pack

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const SchemaVersion = 1

// Record is the source-neutral representation stored in every pack.
type Record struct {
	Source       string   `json:"source,omitempty"`
	SourceID     string   `json:"source_id,omitempty"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name,omitempty"`
	HouseNumber  string   `json:"house_number,omitempty"`
	Street       string   `json:"street,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	Postcode     string   `json:"postcode,omitempty"`
	LocalityType string   `json:"locality_type,omitempty"`
	Locality     string   `json:"locality,omitempty"`
	District     string   `json:"district,omitempty"`
	Region       string   `json:"region,omitempty"`
	CountryCode  string   `json:"country_code,omitempty"`
	Country      string   `json:"country,omitempty"`
	Latitude     float64  `json:"lat"`
	Longitude    float64  `json:"lon"`
	Importance   float64  `json:"importance,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	DisplayName  string   `json:"display_name,omitempty"`
	SearchText   string   `json:"-"`
	Fingerprint  string   `json:"-"`
}

func (r *Record) Prepare() error {
	if math.IsNaN(r.Latitude) || math.IsInf(r.Latitude, 0) || r.Latitude < -90 || r.Latitude > 90 {
		return fmt.Errorf("invalid latitude %v", r.Latitude)
	}
	if math.IsNaN(r.Longitude) || math.IsInf(r.Longitude, 0) || r.Longitude < -180 || r.Longitude > 180 {
		return fmt.Errorf("invalid longitude %v", r.Longitude)
	}
	r.Kind = strings.ToLower(strings.TrimSpace(r.Kind))
	if r.Kind == "" {
		if r.HouseNumber != "" || r.Street != "" {
			r.Kind = "address"
		} else {
			r.Kind = "place"
		}
	}
	r.CountryCode = strings.ToUpper(strings.TrimSpace(r.CountryCode))
	if len(r.CountryCode) > 2 {
		r.CountryCode = r.CountryCode[:2]
	}
	if r.DisplayName == "" {
		r.DisplayName = BuildDisplayName(*r)
	}
	parts := []string{r.Name, r.Street, r.HouseNumber, r.Unit, r.Postcode, r.Locality, r.District, r.Region, r.CountryCode, r.Country}
	parts = append(parts, r.Aliases...)
	r.SearchText = Normalize(strings.Join(parts, " "))
	if r.SearchText == "" {
		return fmt.Errorf("record has no searchable address or name")
	}
	r.Fingerprint = strings.Join([]string{
		r.Kind, r.CountryCode, Normalize(r.Postcode), Normalize(r.Locality), Normalize(r.Street),
		Normalize(r.HouseNumber), Normalize(r.Name), fmt.Sprintf("%.5f", r.Latitude), fmt.Sprintf("%.5f", r.Longitude),
	}, "|")
	return nil
}

func BuildDisplayName(r Record) string {
	var parts []string
	first := strings.TrimSpace(strings.Join(nonEmpty(r.Name, strings.TrimSpace(strings.Join(nonEmpty(r.Street, r.HouseNumber), " "))), ", "))
	if first != "" {
		parts = append(parts, first)
	}
	city := strings.TrimSpace(strings.Join(nonEmpty(r.Postcode, r.Locality), " "))
	parts = append(parts, nonEmpty(city, r.District, r.Region, r.Country)...)
	return strings.Join(unique(parts), ", ")
}

func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if _, ok := seen[key]; value == "" || ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

// Normalize produces an additional search form. It intentionally expands common
// German spellings before stripping other diacritics.
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", "æ", "ae", "œ", "oe")
	value = replacer.Replace(value)
	value = norm.NFD.String(value)
	var b strings.Builder
	space := true
	for _, r := range value {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	tokens := strings.Fields(b.String())
	for i, token := range tokens {
		switch token {
		case "жк":
			tokens[i] = "жилои комплекс" // The preceding diacritic fold also maps й to и.
		case "ул":
			tokens[i] = "улица"
		case "просп":
			tokens[i] = "проспект"
		}
	}
	return strings.Join(tokens, " ")
}
