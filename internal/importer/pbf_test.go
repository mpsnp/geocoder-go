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
