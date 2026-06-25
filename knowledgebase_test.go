package knowledgebase

import (
	"context"
	"strings"
	"testing"
)

func TestIngestChunksAndDedup(t *testing.T) {
	s := newService()
	s.chunkLen = 50
	s.overlap = 10
	long := strings.Repeat("alpha beta gamma delta ", 20) // > 50 runes → multiple chunks
	doc, changed := s.Ingest(context.Background(), Document{URL: "/a", Title: "A", Text: long})
	if !changed || doc.Hash == "" {
		t.Fatalf("first ingest should change; doc=%+v", doc)
	}
	if len(s.chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(s.chunks))
	}
	n := len(s.chunks)
	// Re-ingest identical content → unchanged, no new chunks.
	_, changed2 := s.Ingest(context.Background(), Document{URL: "/a", Title: "A", Text: long})
	if changed2 {
		t.Fatal("re-ingesting identical content should be unchanged (dedupe)")
	}
	if len(s.chunks) != n {
		t.Fatalf("chunk count changed on dedupe: %d → %d", n, len(s.chunks))
	}
}

func TestChangeDetectionReingests(t *testing.T) {
	s := newService()
	s.Ingest(context.Background(), Document{URL: "/p", Text: "original content here"})
	_, changed := s.Ingest(context.Background(), Document{URL: "/p", Text: "updated content now"})
	if !changed {
		t.Fatal("changed content should re-ingest")
	}
	if got := s.Documents("")[0].Text; got != "updated content now" {
		t.Fatalf("doc not updated: %q", got)
	}
}

func TestKeywordSearchFindsRightDoc(t *testing.T) {
	s := newService()
	ctx := context.Background()
	s.Ingest(ctx, Document{URL: "/install", Title: "Install", Text: "how to install the togo cli on macos and linux"})
	s.Ingest(ctx, Document{URL: "/billing", Title: "Billing", Text: "configure stripe payments and invoices"})
	hits := s.Search(Query{Text: "install cli", TopK: 1})
	if len(hits) == 0 || hits[0].DocID != "/install" {
		t.Fatalf("expected /install top hit, got %+v", hits)
	}
}

func TestVectorAndHybridRanking(t *testing.T) {
	s := newService()
	ctx := context.Background()
	s.Ingest(ctx, Document{URL: "/cats", Text: "cats are small domesticated felines that purr"})
	s.Ingest(ctx, Document{URL: "/cars", Text: "cars are motor vehicles with engines and wheels"})
	// Query overlapping the cats doc tokens — hybrid (kw+vec) should rank it first.
	hits := s.Search(Query{Text: "domesticated felines purr", TopK: 2})
	if len(hits) == 0 || hits[0].DocID != "/cats" {
		t.Fatalf("expected /cats first, got %+v", hits)
	}
	if hits[0].Score <= 0 {
		t.Fatalf("expected positive fused score, got %v", hits[0].Score)
	}
}

func TestCollectionIsolation(t *testing.T) {
	s := newService()
	ctx := context.Background()
	s.Ingest(ctx, Document{URL: "/x", Collection: "a", Text: "shared keyword apple"})
	s.Ingest(ctx, Document{URL: "/x", Collection: "b", Text: "shared keyword apple"})
	hitsA := s.Search(Query{Text: "apple", Collection: "a", TopK: 5})
	for _, h := range hitsA {
		if !strings.HasPrefix(h.DocID, "a|") {
			t.Fatalf("collection a search leaked: %+v", h)
		}
	}
	if len(hitsA) != 1 {
		t.Fatalf("collection a should have 1 hit, got %d", len(hitsA))
	}
}

func TestCrawlWithFetcher(t *testing.T) {
	s := newService()
	calls := 0
	s.AddSource(Source{Name: "docs", URL: "https://example.com", Collection: "docs"},
		func(ctx context.Context, url string) (Document, error) {
			calls++
			return Document{URL: url, Title: "Docs", Text: "stable documentation body"}, nil
		})
	changed, err := s.Crawl(context.Background(), "docs")
	if err != nil || !changed {
		t.Fatalf("first crawl should ingest: changed=%v err=%v", changed, err)
	}
	// Second crawl, same content → change detection skips.
	changed2, _ := s.Crawl(context.Background(), "docs")
	if changed2 {
		t.Fatal("second crawl of identical content should be unchanged")
	}
	if calls != 2 {
		t.Fatalf("fetcher should be called each crawl, got %d", calls)
	}
	// Unknown source errors.
	if _, err := s.Crawl(context.Background(), "nope"); err == nil {
		t.Fatal("unknown source should error")
	}
}
