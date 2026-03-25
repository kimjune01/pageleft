# Roadmap

What exists, what's next, and what's blocked.

PageLeft is a search engine for ideas that chose to be free. Semantic search over copyleft-licensed pages, with attribution intact. Crawl, embed, quality, and link analysis are separate pipes that write to PageLeft's store via the API.

## The search pipe

**Goal**: surface the most relevant copyleft-licensed source for a query, preserving the attribution chain that LLMs strip.

### Done

- Semantic search over copyleft-licensed pages (CC BY-SA, AGPL, GPL, etc.)
- Per-paragraph chunk embeddings (BGE-small-en-v1.5, 384D) with 50-char minimum to filter nav fragments
- BGE query prefix: `EmbedQuery()` prepends the retrieval instruction BGE expects, widening score discrimination from stdev 0.03 to 0.07
- PageRank from inter-page link graph
- Compilable flag: 2x ranking boost, `&compiles` search filter
- Source-diverse DPP reranker: overfetch 5x, greedy selection using `Similarity * (floor + (1 - floor) * (1 - maxSim))`. Same-domain candidates get a 0.3 similarity penalty, spreading results across sources. Relevance floor at 0.7 prevents diversity from burying highly relevant results. One kernel handles embedding diversity, source diversity, and relevance preservation.
- Snippet highlighting: return the matching chunk text with query terms bolded
- Version tracking: git SHA embedded via ldflags, `pageleft version` command, exposed in `/api/stats`

### Open problems

- **Score balancing**: final rank is `semantic * (1 + log(1 + rank * n)) * quality * compilable_boost`. The relative weight between semantic similarity and PageRank is implicit, not tuned. No evaluation set exists yet. Prescription: Consolidate from the [parts bin](https://june.kim/the-parts-bin) — collect implicit feedback, fit a learning-to-rank model, update the weight vector.
- **Storage**: SQLite with JSON arrays for embeddings. Linear scan works at ~1,600 pages. Estimated ~50K pages before needing ANN. Candidates: [fogfish/hnsw](https://github.com/fogfish/hnsw) or [TFMV/hnsw](https://github.com/TFMV/hnsw) (both MIT, pure Go).
- **Compilation mode**: the compilable flag is currently a boolean (page has a reference implementation or it doesn't). Two compilation modes exist: **artifact** (page compiles into code, visualization, simulation) and **judgment** (loading page into context improves agent output on domain-specific tasks). Extend the schema from `compilable bool` to `compilation_mode text` (`artifact`, `judgment`, or null). Search filter `&compiles` matches either mode. New filter `&compiles=artifact` or `&compiles=judgment` for mode-specific queries. See [public-domain.md](public-domain.md) for the criteria and [Theory Is Load-Bearing](https://june.kim/theory-is-load-bearing) for the evidence that judgment-mode compilation is real.

## Supporting pipes

Crawl and embed run on federated workers — PageLeft serves the work queue and accepts results but doesn't incur the compute. Quality reviews and link analysis also run externally. Each pipe has its own goal and writes to PageLeft's store via the API.

At current scale (~1,600 pages), I am Attend and Consolidate. I review pages, tune ranking weights, and decide crawl priorities by hand. The supporting pipes automate Perceive through Filter. Automating Attend and Consolidate waits for enough data to justify it.

### Crawl pipe

**Goal**: grow the index with new copyleft-licensed pages.

The crawl pipe maps to the six-stage pipeline from the [parts bin](https://june.kim/the-parts-bin). Each stage has a contract; the crawl pipe's job is to implement all six for URL→page ingestion.

| Stage | Crawl pipe role | Contract | Parts bin algorithm |
|---|---|---|---|
| Perceive | Fetch page, parse HTML | Raw bytes → structured DOM | HTML parsing, Wikipedia REST API |
| Cache | Dedup by URL + content_hash | Retrievable, no duplicates | Hash index (URL), Rabin fingerprint (content_hash) |
| Filter | License check, domain block, robots.txt | Strictly smaller: only copyleft passes | Predicate filtering (WHERE) via `frontier_blocked_domains.txt`, `copyleft_domains.txt` |
| Attend | Frontier prioritization | Best URL first | Missing — currently FIFO |
| Consolidate | Recrawl policy, freshness decay | Parameters update from observed data | Missing — no recrawl loop |
| Remember | Write to pages/chunks/frontier | Lossless append | WAL (SQLite), `AddToFrontier` |

**Done** (Perceive, Cache, Filter, Remember):
- Federated work queues, robots.txt, license detection via `<meta>`, `dc.rights`, and copyleft domain allowlist
- Wikipedia/Wikimedia fetched via REST API. Wiki→wiki excluded from frontier (depth-1 policy)
- Frontier filter on write: `AddToFrontier` rejects already-indexed, non-HTTP, and `frontier_blocked_domains.txt` entries. `prune-frontier` command for one-time cleanup. Reduced frontier from 55K → 32K.
- Domain lists as embedded text files: `blocked_domains.txt` (indexing), `copyleft_domains.txt` (license bypass), `frontier_blocked_domains.txt` (frontier filter)
- Content dedup via `content_hash` (SHA-256 of response body). `ON CONFLICT(url) DO NOTHING` prevents re-indexing known URLs.

**Next: Attend** (frontier prioritization)
- The frontier is a flat table with no priority signal. Workers get FIFO-ordered entries. After pruning, 32K entries remain — 14K gwern, 2.2K LibreTexts, 2.4K US Code. A worker has no way to know that LibreTexts (CC BY-SA STEM textbooks) is higher-value than a random gwern essay.
- Add a `priority` column to the frontier table. Score on insert:
  - Copyleft-domain URLs (in `copyleft_domains.txt`): priority 10
  - URLs linked from multiple indexed pages: priority = inbound count
  - Everything else: priority 1
- `GET /api/frontier` returns `ORDER BY priority DESC, id ASC`. Workers drain highest-priority first.
- Algorithm: **weighted composite score** (parts bin: Attend × flat → MMR). Three signals, one score:
  ```
  priority = w_license * copyleft_known + w_links * log(1 + inbound_count) + w_novelty * (1 - max_sim_to_index)
  ```
  - `copyleft_known`: 1 if domain is in `copyleft_domains.txt`, 0 otherwise. Avoids wasting crawl budget on pages that will fail license verification.
  - `inbound_count`: how many indexed pages link to this URL. Pages linked from multiple sources are more likely authoritative. Log-scaled so one heavily-linked page doesn't dominate.
  - `max_sim_to_index` (optional, deferred): cosine similarity of URL's domain centroid to existing coverage. Prioritizes domains that fill gaps in the index. Requires page embeddings per domain — revisit when the index has 10K+ pages across 100+ domains.
  - Start with `w_license=10, w_links=1, w_novelty=0`. Tune after observing crawl yield (fraction of frontier URLs that pass license verification and produce chunks).
- At 32K entries this is a flat scan on read — no index needed. At 100K+, add a B-tree index on `priority` (parts bin: Cache × sequence → B-tree).

**Later: Consolidate** (recrawl + freshness)
- Push stale pages back onto the frontier, sorted by oldest `crawled_at`. Same fetch pipeline, different seed. One mechanism handles freshness, invalidation, and tombstoning:
  - 200 + same `content_hash` → bump `crawled_at`, done
  - 200 + new `content_hash` → re-chunk, null old embeddings, re-enter embed work queue
  - 301/302 → update canonical URL, merge with existing entry at target
  - 4xx (after 2-3 consecutive failures) → null embeddings (drops from search), keep page row
  - 5xx → do nothing, server might be temporarily down
  - Conditional GET (`ETag`, `Last-Modified`) to skip unchanged pages cheaply
- Freshness as ranking signal: multiply rank by decay factor based on `crawled_at` staleness. Recently verified pages rank higher.
- Algorithm: **exponential decay + EMA convergence gate** (parts bin: Consolidate × flat → EMA convergence gate).
  - Each page has a `freshness` score: `exp(-λ * days_since_crawl)`. λ chosen so freshness halves every 90 days.
  - Final search rank multiplied by freshness. A page crawled yesterday has freshness ≈ 1.0; one crawled a year ago ≈ 0.06.
  - Recrawl priority = `(1 - freshness) * pagerank`. High-authority stale pages recrawl first. Low-authority stale pages recrawl last or never.
  - Convergence gate: if a page's `content_hash` hasn't changed across N consecutive recrawls, extend its recrawl interval exponentially (1 week → 2 weeks → 1 month → 3 months). Stable pages don't need frequent checking. Parts bin: this is EMA applied to change frequency — the recrawl interval converges to the page's actual update rate.
  - Implementation: add `last_crawled_at`, `recrawl_interval`, `consecutive_unchanged` columns to pages. A cron job (or `pageleft recrawl` command) queries pages where `now - last_crawled_at > recrawl_interval`, pushes them to the frontier with priority based on recrawl urgency.

**Next: Code forge indexing** (GitHub, Codeberg)

GitHub and Codeberg repos are copyleft content when their license file says so. Currently blocked because HTML-based license detection doesn't work on forge pages. A new Perceive path:

1. **Detect forge URL**: `github.com/{owner}/{repo}`, `codeberg.org/{owner}/{repo}`.
2. **Check license via API**:
   - GitHub: `GET https://api.github.com/repos/{owner}/{repo}/license` → `license.spdx_id`. Match against copyleft set: `GPL-2.0`, `GPL-3.0`, `AGPL-3.0`, `LGPL-*`, `MPL-2.0`, `CC-BY-SA-*`, `GFDL-*`.
   - Codeberg (Gitea API): `GET https://codeberg.org/api/v1/repos/{owner}/{repo}` → `.license` field. Same SPDX matching.
3. **Index README + docs**: fetch `README.md` via raw URL (`raw.githubusercontent.com` / Codeberg raw). Chunk as prose (paragraph-level, 50-char floor). Optionally index files in `docs/` directory.
4. **Don't index source code yet**: code chunking (function-level, AST-aware) is a separate problem. Start with documentation, which is prose and works with the existing chunking pipeline.
5. **Unblock `github.com` and `codeberg.org`** from `frontier_blocked_domains.txt` but only allow URLs matching the `/{owner}/{repo}` pattern. Subpaths like `/issues`, `/pulls`, `/actions` stay blocked.

Parts bin: this is a new Perceive codec (forge API → structured repo metadata) feeding into the existing Cache/Filter/Remember pipeline. The license check is Filter × predicate (SPDX match against copyleft set).

**Other improvements**:
- Domain blocklist expansion: [UT1 blacklists](https://dsi.ut-capitole.fr/blacklists/) (CC BY-SA). Flat lookup, zero inference cost.
- Crawl discovery: accept sitemap.xml and RSS feeds as Perceive sources.
- Bloom filter for URL dedup: `AddToFrontier` currently does a full `GetPageByURL` query. At scale, a probabilistic cache (Bloom filter, parts bin: Cache × probabilistic) avoids the DB round-trip. False positives are safe (skip a URL that wasn't indexed), false negatives impossible.

**Open**: license detection misses prose/footer declarations. False negatives are safe; false positives are not.

### Embed pipe

**Goal**: make every page searchable by producing chunk-level embeddings.

**Done**: unified embed work queue (`{chunk_id, page_id, text}`), auto-chunking on demand. Auto page embedding: when the last chunk embedding arrives via `POST /api/contribute/embedding`, the server averages chunk embeddings into a page-level embedding. No manual backfill needed.

**Next**:
- Embedding invalidation handled by recrawl-via-frontier (see crawl pipe above). When `content_hash` changes, old chunk embeddings are nulled and new chunks enter the work queue. This is a Remember → Perceive handshake break — stale embeddings violate the contract. See [The Handshake](https://june.kim/the-handshake).

**Later**: multi-model embeddings — accept vectors from different models, normalize to a common similarity space.

### Quality pipe

**Goal**: set the structural floor so pages without substance don't pollute search results.

Quality is not a page-level score that an LLM assigns. Originality, novelty, and diversity are relational — they depend on what else is in the result set. DPP handles all three at search time. The quality pipe's job is narrower: gate out pages that have no substance at all (photos, nav junk, empty shells). The structural heuristic scorer does this without LLM calls.

**Done**:
- Structural heuristic scorer (`rank-agg-v1`): code fences, equations, citations, headings, domain tier. Rank aggregation produces 0-1 scores. Cold start complete — 1,598 pages scored.
- Compounding quality scores (geometric mean), random sampling, quality_coverage metric, anonymous contributor leaderboard.
- Worker client (`quality_scorer.py`): pulls from `/api/work/quality`, scores, submits.

**Next**:
- Raise quality_coverage threshold from 1 to 3 when federated quality workers are active. Currently the structural scorer is the only reviewer, so requiring 3+ reviews makes the metric meaningless. The threshold gates the `/api/stats` coverage number, not the ranking formula.
- Time-decayed quality: a 0.9 review from six months ago weighs less than a 0.7 from today. Decay is a Consolidate operation — read review timestamps, write decayed scores. See [parts bin Consolidate catalog](https://june.kim/the-parts-bin#catalog).

**Open**: review gaming — random sampling and dedup prevent naive Sybil attacks. Coordinated attackers can still inflate scores.

**Not planned**: LLM-based quality scoring. Subjective rubrics are exploit surfaces (see [slop-detection](https://june.kim/slop-detection)). Structural heuristics set the floor; DPP handles the rest at search time.

### Link pipe

**Goal**: measure authority so well-linked sources rank above isolated ones.

**Done**: link extraction, iterative PageRank (damping=0.85, 50 iterations).

**Open**: network bias — PageRank favors well-linked authors. A brilliant page with no inbound links ranks poorly. DPP in embedding space partially compensates — an unlinked page with a novel idea still gets selected for diversity. But the bias is structural.

### Feed reader pipe (not started)

**Goal**: discover fresh copyleft content from feeds instead of manual URL submission.

Uses the [embedding pipe](https://june.kim/embedding-pipe) pattern: Perceive (parse feed entries) → Cache (dedup by URL) → Filter (novelty via distance to existing coverage) → Remember (write to PageLeft's store). See [embedding pipe § Example](https://june.kim/embedding-pipe#example-article-feed).

## Eviction (store-level, not pipe-level)

Pages that don't contribute to provenance or quality decay quietly. Redundant chunks (near-duplicates of higher-quality sources) lose quality over time. Dead pages (consecutive 4xx on recrawl) get their embeddings nulled — they stop appearing in search but stay in the index for dedup. See recrawl-via-frontier in the crawl pipe for the mechanism. Revoked licenses drop quality to zero. Below a threshold, the page is wiped. No graveyard, no archival — if the page mattered, it would have earned quality from reviews or links.

**Embedding eviction for low-quality sources**: when a page's quality drops below a threshold, null its chunk embeddings so it stops appearing in search results. Keep the page row and content_hash in the index — the page is still known, still deduped, still prevents re-crawl. It just doesn't pollute the search space. If quality recovers (new reviews, updated content), re-embed from the work queue. This is compaction, not deletion: the index stays complete, the search stays clean.

## Not planned

- **Web UI**: this is an A2A protocol.
- **Accounts / auth**: contributions are anonymous by design.
- **Moderation dashboard**: quality scores compound. Low-quality pages sink.
- **Popularity-based eviction**: PageLeft keeps what others won't cite.
- **LLM quality axes**: subjective, prompt-relative, and gameable. See [slop-detection](https://june.kim/slop-detection).
