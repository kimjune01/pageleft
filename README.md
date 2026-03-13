# PageLeft

Semantic search over copyleft-licensed web pages.

[pageleft.cc](https://pageleft.cc) | [Manifesto](https://www.june.kim/pageleft-manifesto)

## API

### Search

```
GET /api/search?q=<query>&limit=20&compiles
```

- Chunks: each page is split into paragraphs, embedded separately. Queries match the specific paragraph, not a page average.
- Ranking: `semantic_score` (cosine sim) * `rank_score` (PageRank) * `quality` (compounding review scores). Compilable pages get 2x.
- If `q` is a URL, PageLeft fetches, verifies copyleft license, embeds, and indexes in one request.

**Params:**
- `q` — search query or URL to index
- `limit` — max results (default 20)
- `compiles` — return only pages with reference implementations

### Federated workers

Workers donate crawl, embedding, and quality-review compute. Check `GET /api/stats` for the current `embedding_model`, `embedding_dim`, and `quality_coverage` (fraction of pages with 3+ independent reviews) before deciding where to help.

1. `GET /api/frontier?limit=10` — claim URLs to crawl
2. `POST /api/contribute/page` — submit crawled page (license is re-verified server-side)
3. `GET /api/work/embed?limit=10` — claim chunks that need embeddings (returns `model`, `dim`, and `items` with `chunk_id`, `page_id`, `text`)
4. `POST /api/contribute/embedding` — submit computed embedding (with `chunk_id` or `page_id` for backward compat)
5. `GET /api/work/quality?limit=10` — claim random pages for quality review (returns `page_id`, `url`, `title`, `text_content`)
6. `POST /api/contribute/quality` — submit quality score (`page_id`, `score` 0.0–1.0, `model` used). Each score compounds into the page's `quality` factor, which scales search ranking. No binary eviction — low-quality pages sink gradually.
7. `POST /api/contribute/compilable` — flag a page as compilable (`page_id`, `compilable` bool). Pages with reference implementations get a 2x ranking boost.

### Leaderboard

```
GET /api/leaderboard?type=review&n=10
```

Anonymous contributor rankings by hashed fingerprint. Filter by `type` (`review`, `embed`, `crawl`) or omit for all. `n` defaults to 10.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) or read [Why Contribute](https://www.june.kim/why-contribute).

## Reference implementations

These projects were found by Claude Opus 4.6 via web search, filtered to permissive licenses (MIT, Apache 2.0, BSD) so they can be studied without license conflict. None of this code is copied into PageLeft; they exist here as reading material for anyone building similar things.

### Crawler

- [Colly](https://github.com/gocolly/colly) — Go, Apache 2.0. Callback-based crawling framework.
- [Openverse](https://github.com/WordPress/openverse) — Python, MIT. The only production system that crawls the web for CC-licensed content. Reference for license detection.
- [Crawl4AI](https://github.com/unclecode/crawl4ai) — Python, Apache 2.0. LLM-friendly crawler with built-in content filtering.

### Embeddings and vector search

- [FAISS](https://github.com/facebookresearch/faiss) — C++/Python, MIT. Similarity search at billion scale.
- [Chroma](https://github.com/chroma-core/chroma) — Python, Apache 2.0. Embedding database for AI apps.
- [Qdrant](https://github.com/qdrant/qdrant) — Rust, Apache 2.0. Production vector search engine.

### PageRank

- [dcadenas/pagerank](https://github.com/dcadenas/pagerank) — Go, MIT. Minimal: `graph.Link(from, to, weight)` then `graph.Rank()`.
- [alixaxel/pagerank](https://github.com/alixaxel/pagerank) — Go, MIT. Same idea, adds weighted edges.
- [networkx.pagerank](https://github.com/networkx/networkx) — Python, BSD-3-Clause. Battle-tested graph library with built-in PageRank.

### Combined embeddings + graph ranking

- [txtai](https://github.com/neuml/txtai) — Python, Apache 2.0. Unions vector indexes with graph networks.
- [HippoRAG](https://github.com/OSU-NLP-Group/HippoRAG) — Python, MIT. Personalized PageRank on knowledge graphs for retrieval.

## License

AGPL-3.0-or-later
