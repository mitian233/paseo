package mermaid

import (
	"bytes"
	"context"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func TestCacheIsBoundedAndDoesNotExposeStoredBytes(t *testing.T) {
	ctx := context.Background()
	r := NewRenderer(1, 1<<20)
	q := Request{Source: "sequenceDiagram\n Alice->>Bob: Hello", Font: goregular.TTF, Scale: 1}
	first, err := r.Render(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(first)
	first[0] ^= 0xff
	again, err := r.Render(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, again) {
		t.Fatal("caller modified cached image")
	}
	q.Source = "sequenceDiagram\n Alice->>Bob: Goodbye"
	other, err := NewRenderer(1, 0).Render(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	otherCost := len(other) + len(q.Source) + len("default") + 128
	if otherCost > r.size {
		r.limit = otherCost
	} else {
		r.limit = r.size
	}
	if _, err := r.Render(ctx, q); err != nil {
		t.Fatal(err)
	}
	if r.size > r.limit {
		t.Fatalf("cache exceeded limit: %d > %d", r.size, r.limit)
	}
	for _, e := range r.entries {
		if e.Value.(entry).key.source == "sequenceDiagram\n Alice->>Bob: Hello" {
			t.Fatal("least recently used image was not evicted")
		}
	}
}
