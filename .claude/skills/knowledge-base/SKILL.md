---
name: knowledge-base
description: Build a RAG knowledge base in a togo app — ingest/chunk/embed documents, hybrid (keyword+vector) search, and scheduled crawling with change detection using the knowledge-base plugin.
---

# togo knowledge-base

Use this skill to add a searchable knowledge base / RAG corpus to a togo app.

## Ingest content
```go
kb, _ := knowledgebase.FromKernel(k)
kb.Ingest(ctx, knowledgebase.Document{URL:"/docs/x", Title:"X", Text:"...", Collection:"docs"})
```
Chunks + embeds automatically; re-ingesting identical text is a no-op (change detection).

## Search
```go
hits := kb.Search(knowledgebase.Query{Text:"...", TopK:5, Collection:"docs"})
```
Hybrid keyword + vector retrieval (RRF). Use the results as RAG context for the `ai` plugin.

## Crawl on a schedule
```go
kb.AddSource(knowledgebase.Source{Name:"blog", URL:"...", Cron:"@daily"}, fetcher) // fetcher wires ai-firecrawl/crawlee/searxng
kb.Crawl(ctx, "blog")  // ingests only on change; schedule with the scheduler plugin
```

## Notes
- Default embedder is a deterministic local one (offline/test-safe); swap a real one with `kb.WithEmbedder(...)`.
- For pgvector + BM25 persistence at scale, back it with `rag-postgres`.
- Keep collections per tenant/topic for isolation.
