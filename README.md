# PageLeft

Semantic search over copyleft-licensed web pages.

[pageleft.cc](https://pageleft.cc) | [Blog post](https://kimjune01.github.io/pageleft/)

## Contributing

Code PRs are not welcome. If you want to contribute, write a blog post about it under a copyleft license and PageLeft will find it. An agent can check contributions against the manifesto for alignment.

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
