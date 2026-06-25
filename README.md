<div align="center">
  <img src=".github/assets/togo-mark.svg" alt="togo" height="64" />
  <h1>togo-framework/knowledge-base</h1>
  <p>
    <a href="https://to-go.dev/marketplace"><img src="https://img.shields.io/badge/marketplace-to--go.dev-1FC7DC" alt="marketplace" /></a>
    <a href="https://pkg.go.dev/github.com/togo-framework/knowledge-base"><img src="https://pkg.go.dev/badge/github.com/togo-framework/knowledge-base.svg" alt="pkg.go.dev" /></a>
    <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT" />
  </p>
  <p><strong>A managed knowledge base for <a href="https://to-go.dev">togo</a> — ingest, chunk, embed & hybrid-search content for RAG.</strong></p>
</div>

## Install

```bash
togo install togo-framework/knowledge-base
```

Turns the thin `ai-firecrawl`/`ai-crawlee`/`ai-searxng` data-source drivers into a real knowledge base: **ingest** documents, **chunk** + **embed** them, run **hybrid search** (keyword + vector, fused by reciprocal-rank-fusion), and **crawl sources on a schedule** with content-hash change detection. Collections keep tenants/topics isolated.

## Usage

```go
kb, _ := knowledgebase.FromKernel(k)

// Ingest — chunks + embeds; returns (doc, changed). Re-ingesting identical
// content is a no-op (dedupe / change detection).
kb.Ingest(ctx, knowledgebase.Document{
    URL: "/docs/intro", Title: "Intro", Text: "...", Collection: "docs",
})

// Hybrid search (keyword + vector, RRF-merged).
hits := kb.Search(knowledgebase.Query{Text: "how do I install the cli", TopK: 5, Collection: "docs"})
for _, h := range hits {
    fmt.Println(h.Score, h.Title, h.Snippet)
}
```

### Scheduled crawl + change detection

```go
kb.AddSource(
    knowledgebase.Source{Name: "blog", URL: "https://site.com/blog", Collection: "blog", Cron: "@daily"},
    func(ctx context.Context, url string) (knowledgebase.Document, error) {
        // wire ai-firecrawl / ai-crawlee here
        return fetchMarkdown(ctx, url)
    },
)
changed, _ := kb.Crawl(ctx, "blog") // ingests only if content changed
```

Pair with the [`scheduler`](https://to-go.dev/plugins/scheduler) plugin to run `kb.Crawl` on each source's cron.

### Embeddings

Ships a deterministic **local embedder** (hashing/bag-of-words) so search works offline and tests are reproducible. Swap a real one for semantic quality:

```go
kb.WithEmbedder(myAIEmbedder) // e.g. backed by the ai plugin's Embed
```

## REST API

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/kb/ingest` | ingest a `{url,title,text,collection}` document |
| `GET` | `/api/kb/search?q=&collection=` | hybrid search |
| `GET` | `/api/kb/documents?collection=` | list documents |
| `GET` | `/api/kb/sources` | list crawl sources |

## Configuration

No required env. The store is a bounded in-memory index (swap a DB/vector store via the seam). For a persistent pgvector + BM25 backend, see [`rag-postgres`](https://to-go.dev/plugins/rag-postgres).

---

<div align="center">
  <h3>Premium sponsors</h3>
  <p>
    <a href="https://id8media.com"><strong>ID8 Media</strong></a> &nbsp;·&nbsp;
    <a href="https://one-studio.co"><strong>One Studio</strong></a>
  </p>
  <p><sub>Support togo — <a href="https://github.com/sponsors/fadymondy">become a sponsor</a>.</sub></p>
</div>
