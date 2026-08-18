package catalog

import (
	"context"
	"testing"
	"time"
)

func TestDemoRegistryShape(t *testing.T) {
	r := Demo()

	if got := r.Groups(); len(got) != 1 || got[0] != "demo" {
		t.Fatalf("groups = %v", got)
	}
	if got := len(r.InGroup("demo")); got != 2 {
		t.Fatalf("got %d views, want 2", got)
	}
	if got := r.All()[0].ID(); got != "demo.text" {
		t.Errorf("first view %q, want demo.text (registration order)", got)
	}
}

func TestDemoText(t *testing.T) {
	res, err := demoText(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	txt, ok := res.(Text)
	if !ok {
		t.Fatalf("got %T, want Text", res)
	}
	if len(txt.Lines) != 1 || txt.Lines[0] != "123" {
		t.Fatalf("lines = %q", txt.Lines)
	}
}

func TestDemoTableHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if _, err := demoTable(ctx); err == nil {
		t.Fatal("want an error when the context expires")
	}
}
