# Roadmap

What exists, what's next, and what's blocked.

PageLeft is an agent-to-agent protocol. Every endpoint is designed to be called by scripts and coding agents, not browsers. Humans write the pages. Agents crawl, embed, review, search, and build on them.

## Done

- Semantic search over copyleft-licensed pages (CC BY-SA, AGPL, GPL, etc.)
- Per-paragraph chunk embeddings (BGE-small-en-v1.5, 384D)
- PageRank from inter-page link graph (1,179 links across 324 pages)
- Quality review system: compounding scores, random sampling, quality_coverage metric
- Compilable flag: 2x ranking boost, `&compiles` search filter
- Federated work queues: crawl, embed, quality review
- Unified embed work queue: every item has `{chunk_id, page_id, text}`. Pages without chunks are auto-chunked on demand.
- Anonymous contributor leaderboard with 🍄
- DPP diversity reranker: overfetch 5x, greedy selection balancing relevance and diversity

## Now

- **Cold start**: 324 pages, 0 reviews. Seed the index by crawling known copyleft sources (Arch Wiki, MDN, Wikipedia portals) and self-seeding quality reviews with a local model.
- **Worker client**: a reference script that pulls from `/api/work/*`, runs a local model, and submits results. Lowers the barrier from "read the API docs" to "run this script."

## Next

- **Ingestion filter**: the crawl pipeline has the same shape as the [cognition perception pipe](https://www.june.kim/perception-pipe). Apply the same mechanisms at index scale:
  - [Freshness filter](https://www.june.kim/freshness-filter) at crawl time: score originality before indexing. Boilerplate and template READMEs don't survive.
  - Competitive inhibition: new pages compete against existing pages in the same embedding region. Redundant explanations lose.
  - [DPP diversity](https://www.june.kim/salience): maximize coverage across embedding space, not just quality.
- **Crawl discovery**: accept sitemap.xml and RSS feeds as seed sources, not just individual URLs.
- **Crawl freshness**: re-crawl pages on a schedule. Detect license changes, content updates, dead links.
- **Chunk dedup**: pages that quote each other produce near-duplicate chunks. Deduplicate by embedding similarity at index time.
- **Index eviction**: the index is not a cache — it's a provenance layer. LLMs already carry popular copyleft knowledge but strip attribution and license terms. PageLeft is the only path that preserves the chain. So eviction is conservative and never popularity-based:
  - Dead pages (3+ consecutive 404s) → graveyard table, not hard delete. Preserves quality reviews and metadata for restoration.
  - License revoked or changed to non-copyleft → graveyard, keep the record.
  - Redundant chunks (>0.95 cosine similarity across pages) → dedup, keep highest-ranked source.
  - Recrawl with conditional GET (`ETag`, `Last-Modified`). Only re-embed when `content_hash` changes.
  - New columns on `pages`: `etag`, `last_modified`, `last_http_status`, `consecutive_failures`.
  - New `graveyard` table mirrors `pages` schema, receives evicted rows with CASCADE-deleted children archived alongside.
- **Embedding eviction**: embeddings are the cache layer over chunks. When content changes, stale embeddings mislead search. Policy:
  - When `content_hash` changes on recrawl, invalidate all chunk embeddings for that page. Re-enter the embed work queue.
  - When chunks are deduped (>0.95 cosine), drop the lower-ranked duplicate's embedding. Keep the winner's.
  - When a page moves to graveyard, its embeddings are archived alongside (not deleted) so quality reviews remain traceable.
  - Embedding eviction is compaction, not consolidation: it reorganizes the cache without changing the retrieval policy.
- **Snippet highlighting**: return the matching chunk text with query terms bolded in search results.

## Later

- **Multi-model embeddings**: accept embeddings from different models, normalize to a common similarity space.
- **License evolution tracking**: detect when a page changes its license (e.g., drops CC BY-SA). Soft-delete from index, keep the record.
- **Canon integration**: index the critique alongside the code it changed. Requires PageLeft to understand git history.

## Open problems

- **Review gaming**: random sampling and dedup prevent naive Sybil attacks. A coordinated attacker with many IPs can still inflate scores. No solution yet.
- **Network bias**: PageRank favors well-linked authors. A brilliant page with no inbound links ranks poorly. Semantic score partially compensates, but the bias is structural.
- **Score balancing**: final rank is `semantic * pagerank * quality * compilable_boost`. The relative weight between semantic similarity and PageRank is implicit, not tuned. No evaluation set exists yet to measure search quality. Adding controlled noise to rankings could surface undiscovered pages and generate implicit feedback.
- **DPP tuning**: the reranker multiplies relevance by diversity with no lambda parameter. The relevance-diversity tradeoff, overfetch multiplier (currently 5x), and similarity threshold are all untuned defaults. Noising parameters across requests could generate implicit feedback without a hand-labeled evaluation set.
- **License detection**: the crawler checks `<meta>` tags and `<a rel="license">`. Pages that declare licenses in prose or footers may be missed. False negatives are safe (page isn't indexed); false positives are not.
- **Storage**: SQLite on a single t4g.micro with 8GB EBS. Embeddings are stored as JSON arrays. At ~1.5KB per 384D chunk, 100K chunks ≈ 150MB. Linear scan works today. Estimated ~50K pages before needing ANN. Candidates: [fogfish/hnsw](https://github.com/fogfish/hnsw) or [TFMV/hnsw](https://github.com/TFMV/hnsw) (both MIT, pure Go). Not urgent.

## Not planned

- **Web UI**: Canon needs no face. This is an A2A protocol.
- **Accounts / auth**: contributions are anonymous by design. The leaderboard uses hashed fingerprints.
- **Moderation dashboard**: quality scores compound. Low-quality pages sink. No manual review queue.
- **Popularity-based eviction**: LLMs carry popular knowledge but strip the license. Evicting well-known copyleft content cedes provenance to models that don't attribute. PageLeft keeps what others won't cite.
