# Roadmap

What exists, what's next, and what's blocked.

PageLeft is a store and a search pipe. It serves one use case: semantic search over copyleft-licensed pages, with attribution intact. Crawl, embed, quality review, and link analysis are separate pipes that write to PageLeft's store. They ship in the same binary but have independent goals and timescales.

## The search pipe

**Goal**: surface the most relevant copyleft-licensed source for a query, preserving the attribution chain that LLMs strip.

### Done

- Semantic search over copyleft-licensed pages (CC BY-SA, AGPL, GPL, etc.)
- Per-paragraph chunk embeddings (BGE-small-en-v1.5, 384D)
- PageRank from inter-page link graph (1,179 links across 324 pages)
- Compilable flag: 2x ranking boost, `&compiles` search filter
- DPP diversity reranker: overfetch 5x, greedy selection balancing relevance and diversity
- Snippet highlighting: return the matching chunk text with query terms bolded

### Open problems

- **Score balancing**: final rank is `semantic * pagerank * quality * compilable_boost`. The relative weight between semantic similarity and PageRank is implicit, not tuned. No evaluation set exists yet. Prescription: Consolidate from the [parts bin](https://june.kim/the-parts-bin) — collect implicit feedback, fit a learning-to-rank model, update the weight vector.
- **DPP tuning**: the reranker has no lambda parameter. Overfetch=5x and similarity threshold are hardcoded. Prescription: replace with MMR (tunable λ) from the [embedding pipe](https://june.kim/embedding-pipe), or add a Consolidate step that tunes λ from click-through.
- **Storage**: SQLite with JSON arrays for embeddings. Linear scan works at 324 pages. Estimated ~50K pages before needing ANN. Candidates: [fogfish/hnsw](https://github.com/fogfish/hnsw) or [TFMV/hnsw](https://github.com/TFMV/hnsw) (both MIT, pure Go).
- **Compilation mode**: the compilable flag is currently a boolean (page has a reference implementation or it doesn't). Two compilation modes exist: **artifact** (page compiles into code, visualization, simulation) and **judgment** (loading page into context improves agent output on domain-specific tasks). Extend the schema from `compilable bool` to `compilation_mode text` (`artifact`, `judgment`, or null). Search filter `&compiles` matches either mode. New filter `&compiles=artifact` or `&compiles=judgment` for mode-specific queries. See [public-domain.md](public-domain.md) for the criteria and [Theory Is Load-Bearing](https://june.kim/theory-is-load-bearing) for the evidence that judgment-mode compilation is real.

## Supporting pipes

Crawl and embed run on federated workers — PageLeft serves the work queue and accepts results but doesn't incur the compute. Quality reviews and link analysis also run externally. Each pipe has its own goal and writes to PageLeft's store via the API.

At current scale (324 pages, 0 reviews), I am Attend and Consolidate. I review pages, tune ranking weights, and decide crawl priorities by hand. The supporting pipes automate Perceive through Filter. Automating Attend and Consolidate waits for enough data to justify it.

### Crawl pipe

**Goal**: grow the index with new copyleft-licensed pages.

**Done**: federated work queues, robots.txt, license detection via `<meta>` tags.

**Next**:
- Domain blocklist: [UT1 blacklists](https://dsi.ut-capitole.fr/blacklists/) (CC BY-SA). Flat lookup, zero inference cost.
- Crawl freshness: re-crawl on schedule. Conditional GET (`ETag`, `Last-Modified`). Re-embed when `content_hash` changes.
- Crawl discovery: accept sitemap.xml and RSS feeds as seed sources.

**Open**: license detection misses prose/footer declarations. False negatives are safe; false positives are not.

### Embed pipe

**Goal**: make every page searchable by producing chunk-level embeddings.

**Done**: unified embed work queue (`{chunk_id, page_id, text}`), auto-chunking on demand.

**Next**:
- Embedding invalidation: when `content_hash` changes on recrawl, null chunk embeddings, re-enter work queue. This is a Remember → Perceive handshake break — stale embeddings violate the contract. See [The Handshake](https://june.kim/the-handshake).

**Later**: multi-model embeddings — accept vectors from different models, normalize to a common similarity space.

### Quality pipe

**Goal**: separate signal from noise so the search pipe ranks good pages above bad ones.

**Done**: compounding quality scores, random sampling, quality_coverage metric, anonymous contributor leaderboard.

**Next**:
- Cold start: 324 pages, 0 reviews. Seed quality reviews with a local model.
- Worker client: reference script that pulls from `/api/work/*`, runs a model, submits results.

**Later**:
- Time-decayed quality: a 0.9 review from six months ago weighs less than a 0.7 from today. Decay is a Consolidate operation — read review timestamps, write decayed scores. See [parts bin Consolidate catalog](https://june.kim/the-parts-bin#catalog).
- Competitive inhibition at index time: when a new chunk lands near an existing chunk (cosine > threshold), compare on decayed quality. Winner stays, loser evicted. Requires decayed quality scores to exist first. See [The Handshake](https://june.kim/the-handshake) competitive core.

**Open**: review gaming — random sampling and dedup prevent naive Sybil attacks. Coordinated attackers can still inflate scores.

### Link pipe

**Goal**: measure authority so well-linked sources rank above isolated ones.

**Done**: link extraction, iterative PageRank (damping=0.85, 50 iterations).

**Open**: network bias — PageRank favors well-linked authors. A brilliant page with no inbound links ranks poorly. Semantic score partially compensates, but the bias is structural.

### Feed reader pipe (not started)

**Goal**: discover fresh copyleft content from feeds instead of manual URL submission.

Uses the [embedding pipe](https://june.kim/embedding-pipe) pattern: Perceive (parse feed entries) → Cache (dedup by URL) → Filter (novelty via distance to existing coverage) → Remember (write to PageLeft's store). See [embedding pipe § Example](https://june.kim/embedding-pipe#example-article-feed).

## Eviction (store-level, not pipe-level)

Pages that don't contribute to provenance or quality decay quietly. Redundant chunks (near-duplicates of higher-quality sources) lose quality over time. Dead pages (consecutive 404s) lose quality on each failed recrawl. Revoked licenses drop quality to zero. Below a threshold, the page is wiped. No graveyard, no archival — if the page mattered, it would have earned quality from reviews or links.

**Embedding eviction for low-quality sources**: when a page's quality drops below a threshold, null its chunk embeddings so it stops appearing in search results. Keep the page row and content_hash in the index — the page is still known, still deduped, still prevents re-crawl. It just doesn't pollute the search space. If quality recovers (new reviews, updated content), re-embed from the work queue. This is compaction, not deletion: the index stays complete, the search stays clean.

## Not planned

- **Web UI**: this is an A2A protocol.
- **Accounts / auth**: contributions are anonymous by design.
- **Moderation dashboard**: quality scores compound. Low-quality pages sink.
- **Popularity-based eviction**: PageLeft keeps what others won't cite.
