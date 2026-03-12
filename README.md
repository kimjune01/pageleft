# PageLeft

Semantic search over copyleft-licensed web pages.

[pageleft.cc](https://pageleft.cc) | [Manifesto](https://www.june.kim/pageleft-manifesto)

## API

### Search

```
GET /api/search?q=<query>&limit=20
```

Each page is split into paragraph-level chunks and each chunk is embedded separately, so queries match the specific paragraph that's relevant. Results are ranked by `semantic_score` (cosine similarity of the best-matching chunk) boosted by `rank_score` (PageRank). Snippets come from the matching paragraph, not a generic page summary.

If `q` is a URL (starts with `http://` or `https://`), PageLeft will fetch the page, verify it has a copyleft license, extract paragraphs, embed them, and include the page in results — all in one request. Pages without a copyleft license are rejected silently.

### Federated workers

Workers donate crawl and embedding compute. The flow:

1. `GET /api/frontier?limit=10` — claim URLs to crawl
2. `POST /api/contribute/page` — submit crawled page (license is re-verified server-side)
3. `GET /api/work/embed?limit=10` — claim chunks that need embeddings (returns `chunk_id`, `page_id`, `text`)
4. `POST /api/contribute/embedding` — submit computed embedding (with `chunk_id` or `page_id` for backward compat)

## Contributing

- **Content**: Write a blog post under a copyleft license. An agent will find it, verify the license, and index it.
- **Code**: PRs are not welcome. Write about what you'd change under a copyleft license — an agent will evaluate it against the manifesto and implement what aligns. See [vibelogging](https://june.kim/vibelogging).
- **Compute**: Run a federated worker to donate crawl and embedding cycles.

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
