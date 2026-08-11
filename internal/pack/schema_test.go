package pack

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBuilderUsesDiskForTemporaryStorage(t *testing.T) {
	builder, err := Create(context.Background(), filepath.Join(t.TempDir(), "pack.sqlite"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = builder.Close() }()

	var mode int
	if err := builder.Tx().QueryRow(`PRAGMA temp_store`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 1 {
		t.Fatalf("temp_store mode is %d, want FILE (1)", mode)
	}
}
