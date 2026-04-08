# Roadmap

What exists, what's next, and what's blocked.

PageLeft is a search engine for ideas that chose to be free. Semantic search over copyleft-licensed pages, with attribution intact. Five pipes (search, crawl, embed, quality, link) write to a shared store via the API. Each pipe maps to the six-stage pipeline from the [parts bin](https://june.kim/the-parts-bin).

## The search pipe

**Goal**: surface the most relevant copyleft-licensed source for a query, preserving the attribution chain that LLMs strip.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | BGE query prefix (`EmbedQuery()`) |
| Cache | ✓ | In-memory chunk cache with dirty flag |
| Filter | ✓ | `&compiles` parameter |
| Attend | ✓ | Source-diverse DPP (relevance floor 0.7, source penalty 0.3) |
| Consolidate | ✗ | Learning-to-rank — needs eval set |
| Remember | N/A | Stateless (results returned, not stored) |

### Done

- Semantic search over copyleft-licensed pages (CC BY-SA, AGPL, GPL, etc.)
- Per-paragraph chunk embeddings (BGE-small-en-v1.5, 384D) with 50-char minimum to filter nav fragments
- BGE query prefix: `EmbedQuery()` prepends the retrieval instruction BGE expects, widening score discrimination from stdev 0.03 to 0.07
- PageRank from inter-page link graph
- Compilable flag: 2x ranking boost, `&compiles` search filter
- Source-diverse DPP reranker: overfetch 5x, greedy selection using `Similarity * (floor + (1 - floor) * (1 - maxSim))`. Same-domain candidates get a 0.3 similarity penalty, spreading results across sources. Relevance floor at 0.7 prevents diversity from burying highly relevant results. One kernel handles embedding diversity, source diversity, and relevance preservation.
- Snippet highlighting: return the matching chunk text with query terms bolded
- Version tracking: git SHA embedded via ldflags, `pageleft version` command, exposed in `/api/stats`
- In-memory chunk cache: loaded on first search, invalidated when embeddings arrive. Double-checked locking avoids reload races.

### Open problems

- **Consolidate (score balancing)**: final rank is `semantic * (1 + log(1 + rank * n)) * quality * compilable_boost`. The relative weight between semantic similarity and PageRank is implicit, not tuned. No evaluation set exists yet. Prescription: collect implicit feedback (which results get clicked/loaded by agents), fit a learning-to-rank model, update the weight vector. Parts bin: Consolidate × flat → gradient descent or delta-bar-delta.
- **Cache (storage)**: SQLite with JSON arrays for embeddings. In-memory cache works at ~2,500 pages / 100K chunks. Estimated ~10K pages before memory pressure. Candidates: [fogfish/hnsw](https://github.com/fogfish/hnsw) or [TFMV/hnsw](https://github.com/TFMV/hnsw) (both MIT, pure Go). Parts bin: Cache × embedding_space → HNSW index.
- **Compilation mode**: the compilable flag is currently a boolean. Two compilation modes exist: **artifact** (page compiles into code, visualization, simulation) and **judgment** (loading page into context improves agent output on domain-specific tasks). Extend the schema from `compilable bool` to `compilation_mode text` (`artifact`, `judgment`, or null). See [public-domain.md](public-domain.md) for the criteria and [Theory Is Load-Bearing](https://june.kim/theory-is-load-bearing) for the evidence that judgment-mode compilation is real.

## Supporting pipes

Crawl and embed run on federated workers — PageLeft serves the work queue and accepts results but doesn't incur the compute. Quality reviews and link analysis also run externally. Each pipe has its own goal and writes to PageLeft's store via the API.

### Crawl pipe

**Goal**: grow the index with new copyleft-licensed pages.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | HTML/PDF parsing, Wikipedia REST API, forge README fetch |
| Cache | ✓ | Hash index (URL dedup), SHA-256 (content dedup) |
| Filter | ✓ | Unified `Resolve()` chain + persistent Bloom filter |
| Attend | ✓ | `log(1 + inbound) * (1 + noise)` frontier priority |
| Consolidate | partial | `prune-stale` revalidates by oldest `last_validated` (manual). EMA convergence gate not yet implemented. |
| Remember | ✓ | SQLite WAL (pages, chunks, frontier tables) |

**Done**:
- Federated work queues, robots.txt, license detection via `<meta>`, `dc.rights`, and copyleft domain allowlist
- Wikipedia/Wikimedia fetched via REST API. Wiki→wiki excluded from frontier (depth-1 policy)
- Unified filter chain: `crawler.Resolve(url)` runs protocol → blocked domain → Bloom filter → forge → Wikipedia → copyleft domain → allow. One function, one decision. Returns `{Action, License, FetchURL, Reason}`.
- Persistent Bloom filter (`nonpermissive.bloom`): learns domains that are neither copyleft nor public domain. 5M capacity (8.6MB), 0.1% FPR. Seeded from `frontier_blocked_domains.txt` + UT1 blacklists (adult, malware, phishing, gambling, shopping, social, ads, shorteners, dating — 4.9M domains). Grows at runtime when pages fail license verification. `seed-blocklist` command imports domain list files.
- GitHub/Codeberg forge indexing: detect `/{owner}/{repo}`, check license via API (SPDX match) with LICENSE file keyword fallback for NOASSERTION repos. Codeberg uses `default_branch` from Gitea API (not hardcoded `main`). Shared `matchLicenseText` for both forges. Fetches raw README only.
- Auto-reindex PageRank: triggers in background goroutine when page count grows >5% since last reindex.
- Frontier Attend: `inbound` column tracks how many indexed pages link to each frontier URL. Priority = `log(1 + inbound) * (1 + uniform(0, 0.1))`. Overfetch 3x, score, sort, return top N. Stochastic noise shuffles within tiers.
- Parallel crawl worker (`crawl_worker.py`): N threads pull from frontier, submit pages, drain embed queue.
- Domain lists as embedded text files: `blocked_domains.txt` (indexing), `copyleft_domains.txt` (license bypass), `frontier_blocked_domains.txt` (frontier filter).
- Content dedup via `content_hash` (SHA-256). `ON CONFLICT(url) DO NOTHING` on pages, `ON CONFLICT(url) DO UPDATE SET inbound = inbound + 1` on frontier.
- **Stale-page revalidation (Layers 0+1)**: `pages` table carries `etag`, `last_modified`, `last_validated`, `consecutive_failures`. The `prune-stale` command iterates pages oldest-validated first and issues conditional GETs (`If-None-Match`, `If-Modified-Since`) against each. Decision tree: 304 → bump validators; 200 same hash → refresh validators; 200 new content → re-extract text and chunks atomically (single transaction); 410 → delete; 404 → increment failures, advance `last_validated`, delete after 3 occurrences; 5xx/timeout → transient skip; off-domain redirect → delete. Forge pages resolve to their raw fetch URL via `crawler.Resolve` so the off-domain check uses the right host. PDF/HTML/text-plain content types all dispatch correctly.

**Next: Consolidate** (federated revalidation + freshness ranking)
- **Layer 2: federate prune-stale through the work queue.** The local decision tree is done; what's missing is distribution and scheduling. New endpoints `GET /api/work/revalidate` (oldest first, NULLs first) and `POST /api/contribute/revalidate` mirror the embed/quality queue pattern. Workers do the conditional GETs and submit results.
- **Redirect consolidation**: same-origin 301/302 should update the stored canonical URL and merge duplicates at the target. Currently treated as a successful fetch of the redirect target without URL update.
- Freshness as ranking signal: multiply rank by decay factor based on `last_validated` staleness. Recently verified pages rank higher.
- Algorithm: **exponential decay + EMA convergence gate** (parts bin: Consolidate × flat → EMA convergence gate).
  - Each page has a `freshness` score: `exp(-λ * days_since_crawl)`. λ chosen so freshness halves every 90 days.
  - Recrawl priority = `(1 - freshness) * pagerank`. High-authority stale pages recrawl first.
  - Convergence gate: if `content_hash` unchanged across N recrawls, extend interval exponentially (1w → 2w → 1m → 3m). Parts bin: EMA applied to change frequency.

**Other improvements**:
- **Feed/sitemap ingestion**: periodically fetch `sitemap.xml` and `feed.xml`/`atom.xml` from known copyleft domains, push discovered URLs into the frontier. Lightweight cron — no new pipe, just a Perceive source for the existing crawl pipe. Priority: copyleft domain allowlist sites first (already trusted, no per-page license detection needed). Store `last_fetched` per feed URL to support conditional GET and avoid re-processing unchanged feeds.
- Bloom filter for URL dedup in `AddToFrontier`: currently does a full `GetPageByURL` query. At scale, a probabilistic cache avoids the DB round-trip. Parts bin: Cache × probabilistic → Bloom filter.
- Bloom filter rebuild on deploy: the persistent `nonpermissive.bloom` learns domains at runtime, but can't *unlearn* when text file lists change (e.g., unblocking `github.com`). Deleting the file and restarting rebuilds from seed lists in ~30s. Automate as a deploy step: if `frontier_blocked_domains.txt` changed since last deploy, delete and re-seed. UT1 re-seed via `seed-blocklist` could also run on a monthly cron.

**Planned: document extraction** — `.docx`, `.xlsx`, `.pptx` URLs are currently blocked from the frontier because the contribute handler only accepts HTML and PDF. Adding extraction support (e.g., via Go libraries for OOXML) would let these through the same path as PDF: text extraction at fetch time, domain-level license required. Unblock the extensions in `binaryExtensions` once extraction is implemented.

**Open**: license detection misses prose/footer declarations. False negatives are safe; false positives are not.

### Embed pipe

**Goal**: make every page searchable by producing chunk-level embeddings.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | Work queue (`GET /api/work/embed`) |
| Cache | ✗ | Needs HNSW at scale (100K+ chunks in linear scan) |
| Filter | N/A | All chunks get embedded |
| Attend | N/A | FIFO |
| Consolidate | ✗ | No embedding invalidation on content change |
| Remember | ✓ | Auto page embedding + quality URL hint |

**Done**: unified embed work queue (`{chunk_id, page_id, text}`), auto-chunking on demand. Auto page embedding: when the last chunk embedding arrives, the server averages chunk embeddings into a page-level embedding and returns `page_complete: true` with a `next.quality` hint so workers can immediately submit quality reviews.

**Next**:
- Embedding invalidation handled by recrawl-via-frontier (see crawl pipe). When `content_hash` changes, old chunk embeddings are nulled and new chunks enter the work queue. This is a Remember → Perceive handshake break — stale embeddings violate the contract. See [The Handshake](https://june.kim/the-handshake).

**Later**: multi-model embeddings — accept vectors from different models, normalize to a common similarity space.

### Quality pipe

**Goal**: set the structural floor so pages without substance don't pollute search results.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | Work queue (`GET /api/work/quality`) |
| Cache | N/A | — |
| Filter | ✓ | Structural heuristic scorer (rank aggregation) |
| Attend | N/A | Scores compound independently |
| Consolidate | ✗ | Time-decayed quality — not started |
| Remember | ✓ | Geometric mean compounding |

Quality is not a page-level score that an LLM assigns. Originality, novelty, and diversity are relational — they depend on what else is in the result set. DPP handles all three at search time. The quality pipe's job is narrower: gate out pages that have no substance at all (photos, nav junk, empty shells). The structural heuristic scorer does this without LLM calls.

**Done**:
- Structural heuristic scorer (`rank-agg-v1`): code fences, equations, citations, headings, domain tier. Rank aggregation produces 0-1 scores.
- Compounding quality scores (geometric mean), random sampling, quality_coverage metric (threshold=1), anonymous contributor leaderboard.
- Worker client (`quality_scorer.py`): pulls from `/api/work/quality`, scores, submits.

**Next**:
- Run quality scorer on new pages — quality_coverage dropped from 99% to 62% after indexing ~1,000 new pages.
- Raise quality_coverage threshold from 1 to 3 when federated quality workers are active.
- Time-decayed quality: a 0.9 review from six months ago weighs less than a 0.7 from today. Parts bin: Consolidate × flat → EMA convergence gate.

**Open**: review gaming — random sampling and dedup prevent naive Sybil attacks. Coordinated attackers can still inflate scores.

**Not planned**: LLM-based quality scoring. Subjective rubrics are exploit surfaces (see [slop-detection](https://june.kim/slop-detection)). Structural heuristics set the floor; DPP handles the rest at search time.

### Link pipe

**Goal**: measure authority so well-linked sources rank above isolated ones.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | `ExtractLinks()` during page indexing |
| Cache | ✓ | `links` table with UNIQUE constraint |
| Filter | partial | Links to non-indexed pages go to frontier, not links table |
| Attend | N/A | PageRank is global, not per-query |
| Consolidate | ✓ (manual) | Iterative PageRank — `reindex` command |
| Remember | ✓ | `InsertLink()`, `UpdatePageRank()` |

**Done**: link extraction, iterative PageRank (damping=0.85, 50 iterations). Links discovered during page indexing go to the frontier if the target isn't indexed yet.

**Next**: auto-reindex PageRank after batch indexing. Currently requires manual `pageleft reindex` command. Could trigger automatically when page count increases by >5% since last reindex.

**Open**: network bias — PageRank favors well-linked authors. A brilliant page with no inbound links ranks poorly. DPP in embedding space partially compensates — an unlinked page with a novel idea still gets selected for diversity. But the bias is structural.

### Domain discovery (not started)

**Goal**: grow the copyleft domain list automatically instead of manual curation.

Copyleft content is sparse on the web. Spidering outward from known sources hits diminishing returns fast — most outbound links point to non-copyleft sites. The growth bottleneck isn't crawling or feed ingestion, it's finding new domains that publish copyleft content at all.

**Sources to mine**:
- Creative Commons search / CC directory listings
- Wikipedia external links from copyleft-licensed articles (outbound links from CC BY-SA pages often point to CC BY-SA sources)
- Open textbook directories (OpenStax, Open Textbook Library)
- AGPL/GPL project documentation on custom domains (not just GitHub — hosted docs sites)
- Academic open-access repositories with CC BY-SA mandates (e.g., DOAJ, PubMed Central OA subset)

**Verification**: fetch homepage, check for site-wide license declaration (`<meta>`, `dc.rights`, footer, `/license` page). If copyleft → add to domain list, auto-discover feed, start ingesting. Conservative: false negatives are safe, false positives are not. Require at least two signals (e.g., meta tag + footer) before trusting a domain-level license claim.

**Command**: `pageleft discover-domains` — runs the mining sources, verifies candidates, appends confirmed domains to `copyleft_domains.txt`.

### Feed reader pipe (not started)

**Goal**: discover fresh copyleft content from feeds instead of manual URL submission.

Uses the [embedding pipe](https://june.kim/embedding-pipe) pattern: Perceive (parse feed entries) → Cache (dedup by URL) → Filter (novelty via distance to existing coverage) → Remember (write to PageLeft's store). See [embedding pipe § Example](https://june.kim/embedding-pipe#example-article-feed).

## Eviction (store-level, not pipe-level)

Pages that don't contribute to provenance or quality decay quietly. Redundant chunks (near-duplicates of higher-quality sources) lose quality over time. Dead pages are removed by `prune-stale`: 410 deletes immediately, 404 deletes after 3 consecutive failures across runs (CDN hiccup tolerance), and off-domain redirects delete because the page no longer points at indexable content. Revoked licenses drop quality to zero. Below a threshold, the page is wiped. No graveyard, no archival — if the page mattered, it would have earned quality from reviews or links.

**Embedding eviction for low-quality sources**: when a page's quality drops below a threshold, null its chunk embeddings so it stops appearing in search results. Keep the page row and content_hash in the index — the page is still known, still deduped, still prevents re-crawl. It just doesn't pollute the search space. If quality recovers (new reviews, updated content), re-embed from the work queue. This is compaction, not deletion: the index stays complete, the search stays clean.

## Not planned

- **Web UI**: this is an A2A protocol.
- **Accounts / auth**: contributions are anonymous by design.
- **Moderation dashboard**: quality scores compound. Low-quality pages sink.
- **Popularity-based eviction**: PageLeft keeps what others won't cite.
- **LLM quality axes**: subjective, prompt-relative, and gameable. See [slop-detection](https://june.kim/slop-detection).
