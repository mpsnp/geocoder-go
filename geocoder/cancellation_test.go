package geocoder

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Cancel after several CPU checkpoints, without timing-sensitive sleeps.
type checkpointContext struct {
	context.Context
	calls int
}

func (c *checkpointContext) Err() error {
	c.calls++
	if c.calls > 8 {
		return context.Canceled
	}
	return nil
}
func TestFuzzyCPUChecksCancellation(t *testing.T) {
	ctx := &checkpointContext{Context: context.Background()}
	if _, err := fuzzyQueryVariants(ctx, strings.Repeat("word ", 100)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx = &checkpointContext{Context: context.Background()}
	if distance := levenshtein(ctx, strings.Repeat("a", 10000), strings.Repeat("b", 10000)); distance != -1 {
		t.Fatalf("distance=%d", distance)
	}
	ctx = &checkpointContext{Context: context.Background()}
	if _, ok := fuzzySearchScore(ctx, Result{searchText: strings.Repeat("candidate ", 1000)}, "candidate"); ok {
		t.Fatal("scored cancelled search")
	}
}
