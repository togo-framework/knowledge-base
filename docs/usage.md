# knowledge-base — usage

## Ingest
```go
kb, _ := knowledgebase.FromKernel(k)
doc, changed := kb.Ingest(ctx, knowledgebase.Document{URL:"/p", Title:"P", Text:"...", Collection:"docs"})
```
Chunks (default 800 runes / 120 overlap), embeds each chunk, stores with a
content hash. Re-ingesting identical text returns `changed=false` (dedupe).

## Search (hybrid)
```go
hits := kb.Search(knowledgebase.Query{Text:"install cli", TopK:5, Collection:"docs"})
```
Keyword (term-frequency) and vector (cosine over the embedder) rankings are
merged with reciprocal-rank fusion; best chunk per document wins.

## Embeddings
Default is a deterministic local hashing embedder (offline + test-friendly).
Swap for a real one: `kb.WithEmbedder(myEmbedder)` (implements `Embed(text) []float64`).

## Crawl sources
```go
kb.AddSource(knowledgebase.Source{Name:"blog", URL:"https://site/blog", Collection:"blog", Cron:"@daily"}, fetcher)
changed, _ := kb.Crawl(ctx, "blog") // ingests only on content change
```
Wire `fetcher` to ai-firecrawl/ai-crawlee/ai-searxng, and schedule `Crawl` with the scheduler plugin.

## REST
`POST /api/kb/ingest`, `GET /api/kb/search?q=&collection=`, `GET /api/kb/documents`, `GET /api/kb/sources`.

## Persistence
The default store is in-memory. For pgvector + BM25 persistence, use `rag-postgres`
as the backing store, or implement the store seam.
