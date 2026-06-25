// Package knowledgebase is a managed knowledge base for togo: ingest documents,
// chunk + embed them, and run hybrid (keyword + vector) search for RAG. It can
// also crawl sources on a schedule with content-hash change detection.
//
//	kb, _ := knowledgebase.FromKernel(k)
//	kb.Ingest(ctx, knowledgebase.Document{URL: "/docs/intro", Title: "Intro", Text: "..."})
//	hits := kb.Search(knowledgebase.Query{Text: "how do I install", TopK: 5})
package knowledgebase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"
)

const embedDim = 96

// Embedder turns text into a vector. The default is a deterministic local
// hashing embedder (works offline / in tests); wire a real one (e.g. via the
// ai plugin) with Service.WithEmbedder for semantic quality.
type Embedder interface {
	Embed(text string) []float64
}

// Document is a unit of source content.
type Document struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Text       string    `json:"text"`
	Source     string    `json:"source,omitempty"`
	Collection string    `json:"collection,omitempty"`
	Hash       string    `json:"hash"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type chunk struct {
	docID      string
	collection string
	text       string
	terms      map[string]int
	vec        []float64
}

// Query parameters for Search.
type Query struct {
	Text       string
	TopK       int
	Collection string
}

// Result is a ranked search hit.
type Result struct {
	DocID   string  `json:"doc_id"`
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

// Fetcher retrieves a document for a source URL (wire an ai data-source plugin).
type Fetcher func(ctx context.Context, url string) (Document, error)

// Source is a crawlable origin with optional change detection.
type Source struct {
	Name       string  `json:"name"`
	URL        string  `json:"url"`
	Collection string  `json:"collection,omitempty"`
	Cron       string  `json:"cron,omitempty"`
	fetch      Fetcher
}

// Service is the knowledge-base runtime (k.Get("knowledge-base")).
type Service struct {
	mu       sync.RWMutex
	docs     map[string]*Document // by ID (url|collection)
	chunks   []*chunk
	sources  map[string]*Source
	embedder Embedder
	chunkLen int
	overlap  int
}

func init() {
	togo.RegisterProviderFunc("knowledge-base", togo.PriorityService, func(k *togo.Kernel) error {
		s := newService()
		k.Set("knowledge-base", s)
		if k.Router != nil {
			s.mountRoutes(k.Router)
		}
		return nil
	})
}

func newService() *Service {
	return &Service{
		docs:     map[string]*Document{},
		sources:  map[string]*Source{},
		embedder: localEmbedder{},
		chunkLen: 800,
		overlap:  120,
	}
}

// FromKernel returns the knowledge-base Service.
func FromKernel(k *togo.Kernel) (*Service, bool) {
	v, ok := k.Get("knowledge-base")
	if !ok {
		return nil, false
	}
	s, ok := v.(*Service)
	return s, ok
}

// WithEmbedder swaps the embedder (e.g. an ai-backed one).
func (s *Service) WithEmbedder(e Embedder) *Service { s.embedder = e; return s }

func docID(url, collection string) string {
	if collection == "" {
		return url
	}
	return collection + "|" + url
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Ingest stores/updates a document: it chunks + embeds the text. It returns
// (doc, true) when content changed and was (re)ingested, or (doc, false) when
// the content hash is unchanged (change detection / dedupe).
func (s *Service) Ingest(ctx context.Context, d Document) (*Document, bool) {
	d.Hash = hashText(d.Text)
	id := docID(d.URL, d.Collection)
	d.ID = id
	d.UpdatedAt = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.docs[id]; ok && existing.Hash == d.Hash {
		return existing, false // unchanged
	}
	// remove old chunks for this doc (re-ingest)
	if _, ok := s.docs[id]; ok {
		kept := s.chunks[:0]
		for _, c := range s.chunks {
			if c.docID != id {
				kept = append(kept, c)
			}
		}
		s.chunks = kept
	}
	rec := &d
	s.docs[id] = rec
	for _, ct := range chunkText(d.Text, s.chunkLen, s.overlap) {
		s.chunks = append(s.chunks, &chunk{
			docID:      id,
			collection: d.Collection,
			text:       ct,
			terms:      termFreq(ct),
			vec:        s.embedder.Embed(ct),
		})
	}
	return rec, true
}

// Search runs hybrid keyword + vector retrieval merged by reciprocal-rank fusion.
func (s *Service) Search(q Query) []Result {
	if q.TopK <= 0 {
		q.TopK = 5
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	qTerms := termFreq(q.Text)
	qVec := s.embedder.Embed(q.Text)

	type scored struct {
		idx int
		val float64
	}
	var kw, vec []scored
	for i, c := range s.chunks {
		if q.Collection != "" && c.collection != q.Collection {
			continue
		}
		if k := keywordScore(qTerms, c.terms); k > 0 {
			kw = append(kw, scored{i, k})
		}
		vec = append(vec, scored{i, cosine(qVec, c.vec)})
	}
	sort.Slice(kw, func(a, b int) bool { return kw[a].val > kw[b].val })
	sort.Slice(vec, func(a, b int) bool { return vec[a].val > vec[b].val })

	const rrfK = 60.0
	fused := map[int]float64{}
	for rank, sc := range kw {
		fused[sc.idx] += 1.0 / (rrfK + float64(rank+1))
	}
	for rank, sc := range vec {
		fused[sc.idx] += 1.0 / (rrfK + float64(rank+1))
	}

	// best chunk per doc
	bestByDoc := map[string]int{}
	bestScore := map[string]float64{}
	for idx, f := range fused {
		d := s.chunks[idx].docID
		if f > bestScore[d] {
			bestScore[d] = f
			bestByDoc[d] = idx
		}
	}
	out := make([]Result, 0, len(bestByDoc))
	for d, idx := range bestByDoc {
		doc := s.docs[d]
		out = append(out, Result{
			DocID:   d,
			URL:     doc.URL,
			Title:   doc.Title,
			Snippet: snippet(s.chunks[idx].text),
			Score:   bestScore[d],
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	if len(out) > q.TopK {
		out = out[:q.TopK]
	}
	return out
}

// Documents lists stored documents (optionally filtered by collection).
func (s *Service) Documents(collection string) []*Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Document
	for _, d := range s.docs {
		if collection == "" || d.Collection == collection {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].UpdatedAt.After(out[b].UpdatedAt) })
	return out
}

// AddSource registers a crawlable source with a fetcher.
func (s *Service) AddSource(src Source, fetch Fetcher) {
	src.fetch = fetch
	s.mu.Lock()
	s.sources[src.Name] = &src
	s.mu.Unlock()
}

// Sources lists registered crawl sources.
func (s *Service) Sources() []*Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Source, 0, len(s.sources))
	for _, src := range s.sources {
		out = append(out, src)
	}
	return out
}

// Crawl fetches a source and ingests it; returns whether content changed.
func (s *Service) Crawl(ctx context.Context, name string) (bool, error) {
	s.mu.RLock()
	src := s.sources[name]
	s.mu.RUnlock()
	if src == nil || src.fetch == nil {
		return false, &crawlError{name}
	}
	doc, err := src.fetch(ctx, src.URL)
	if err != nil {
		return false, err
	}
	if doc.Collection == "" {
		doc.Collection = src.Collection
	}
	if doc.Source == "" {
		doc.Source = src.Name
	}
	_, changed := s.Ingest(ctx, doc)
	return changed, nil
}

type crawlError struct{ name string }

func (e *crawlError) Error() string { return "knowledge-base: unknown or unfetchable source " + e.name }

// --- text helpers ---

func chunkText(text string, size, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if len(runes) <= size {
		return []string{text}
	}
	var out []string
	step := size - overlap
	if step < 1 {
		step = size
	}
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
	}
	return out
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

func termFreq(s string) map[string]int {
	m := map[string]int{}
	for _, t := range tokenize(s) {
		if len(t) > 1 {
			m[t]++
		}
	}
	return m
}

func keywordScore(q, doc map[string]int) float64 {
	var score float64
	for t, qn := range q {
		if dn, ok := doc[t]; ok {
			score += float64(qn) * (1.0 + math.Log(1.0+float64(dn)))
		}
	}
	return score
}

// localEmbedder is a deterministic hashing embedder (bag-of-words → fixed dim).
type localEmbedder struct{}

func (localEmbedder) Embed(text string) []float64 {
	v := make([]float64, embedDim)
	for _, t := range tokenize(text) {
		h := sha256.Sum256([]byte(t))
		idx := (int(h[0]) | int(h[1])<<8) % embedDim
		sign := 1.0
		if h[2]%2 == 0 {
			sign = -1.0
		}
		v[idx] += sign
	}
	// L2 normalize
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range v {
			v[i] /= norm
		}
	}
	return v
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot // both are unit vectors
}

func snippet(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return string(r)
}

// --- REST ---

func (s *Service) mountRoutes(r chi.Router) {
	r.Route("/api/kb", func(r chi.Router) {
		r.Post("/ingest", func(w http.ResponseWriter, req *http.Request) {
			var d Document
			if err := json.NewDecoder(req.Body).Decode(&d); err != nil {
				writeJSON(w, 400, map[string]string{"error": "bad body"})
				return
			}
			doc, changed := s.Ingest(req.Context(), d)
			writeJSON(w, 200, map[string]any{"document": doc, "ingested": changed})
		})
		r.Get("/search", func(w http.ResponseWriter, req *http.Request) {
			q := Query{Text: req.URL.Query().Get("q"), Collection: req.URL.Query().Get("collection")}
			writeJSON(w, 200, s.Search(q))
		})
		r.Get("/documents", func(w http.ResponseWriter, req *http.Request) {
			writeJSON(w, 200, s.Documents(req.URL.Query().Get("collection")))
		})
		r.Get("/sources", func(w http.ResponseWriter, req *http.Request) {
			writeJSON(w, 200, s.Sources())
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
