package importer

import "testing"

func TestNodeFilterHasNoFalseNegatives(t *testing.T) {
	filter := newNodeFilter(10_000)
	ids := []int64{0, 1, 42, 1<<31 - 1, 1 << 40, 13_000_000_001}
	for _, id := range ids {
		filter.Add(id)
		filter.Add(id)
	}
	for _, id := range ids {
		if !filter.Contains(id) {
			t.Fatalf("filter lost node ID %d", id)
		}
	}
}

func TestNodeFilterSizeIsBounded(t *testing.T) {
	filter := newNodeFilter(1 << 40)
	if size := len(filter.words) * 8; size > maxNodeFilterBytes {
		t.Fatalf("filter uses %d bytes, maximum is %d", size, maxNodeFilterBytes)
	}
}

func TestResidentialAndSettlementClassification(t *testing.T) {
	for _, tc := range []struct{ place, landuse, kind, locality string }{
		{"city", "", "locality", "Example Place"}, {"village", "", "locality", "Example Place"},
		{"suburb", "", "place", ""}, {"neighbourhood", "", "place", ""}, {"", "residential", "place", ""},
	} {
		tags := map[string]string{"name": "Example Place", "place": tc.place, "landuse": tc.landuse}
		if !shouldImportOSM(tags, false) {
			t.Errorf("not selected: %#v", tc)
		}
		record := osmRecord(tags, 0, 0, "way", 1, Options{})
		if record.Kind != tc.kind || record.Locality != tc.locality {
			t.Errorf("%#v: got %s / %s", tc, record.Kind, record.Locality)
		}
	}
	if shouldImportOSM(map[string]string{"landuse": "residential"}, false) {
		t.Fatal("unnamed landuse selected")
	}
}
