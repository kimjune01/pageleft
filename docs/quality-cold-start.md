# Quality Cold Start Plan

1,223 pages, 0 reviews. The ranking formula multiplies by quality — right now that's a no-op. This plan stratifies the index using cheap Perceive → Filter operations before spending any API calls on model reviews.

## Phase 1: Perceive (extract two features from stored text_content)

All features computed from `text_content` already in SQLite. No fetching, no models. One pass over the DB. Two features only — fewer features, less noise.

**Feature 1: Structural score** (regex on text_content, sum of binary/count signals):
- `has_code` — presence of code fences or indented code blocks
- `has_equations` — presence of LaTeX delimiters, mathematical notation
- `has_citations` — presence of reference patterns (RFC NNNN, §NNN, [N], DOI)
- `has_worked_examples` — presence of "Example:", "Proof:", "Solution:", numbered exercises
- `heading_count` — number of headings (structural organization)
- `list_count` — number of lists (spec-like structure)

Sum these into one integer. This is the compilability signal — pages with code, equations, citations, and worked examples are implementation-grade.

**Feature 2: Domain tier** (from URL):
- Tier 1: rfc-editor.org, bartoszmilewski.com, math.dartmouth.edu, gutenberg.org — known high-quality, curated sources
- Tier 2: math.libretexts.org, eng.libretexts.org, phys.libretexts.org, nordstrommath.com, uscode.house.gov, ecfr.gov — textbook/reference quality
- Tier 3: pressbooks.pub subdomains — variable, often introductory
- Tier 4: everything else — unknown

This is curation judgment applied at the domain level. We selected these sources today — that selection *is* quality judgment.

## Phase 2: Filter (rank aggregation, no weights)

**Rank aggregation** instead of Pareto. Pareto with 2-3 dimensions at 1,223 pages produces too many non-dominated pages in layer 0 — doesn't stratify enough. Rank aggregation produces a total ordering.

1. Rank all pages by structural score (1 = highest, 1223 = lowest). Ties share the average rank.
2. Rank all pages by domain tier (tier 1 gets rank ~1, tier 4 gets rank ~1223). Within the same tier, all pages share the same rank.
3. Sum the two ranks per page. Lower sum = higher quality.

No weights — summing ranks treats both features equally. A page that ranks top-100 on structure and is tier-1 domain gets a low sum. A thin Pressbooks stub with no code, no equations, tier 3 gets a high sum.

## Phase 2.5: Fix compounding (prerequisite)

The current quality formula is multiplicative: `quality = quality * score`. This penalizes pages that get more reviews — three 0.9 reviews give 0.729, worse than one 0.8 review. More attention = lower quality is backwards.

Fix: **geometric mean**. `quality = product(all scores) ^ (1/n)`. Three 0.9s → 0.9. More reviews = more confidence, not more penalty.

No new columns needed. The `quality_reviews` table already stores every review. The update becomes:

```sql
UPDATE pages SET quality = POWER(quality * ?, 1.0 / (SELECT COUNT(*) FROM quality_reviews WHERE page_id = ?)) WHERE id = ?
```

One subquery on an indexed column per submission. Nanoseconds at current scale.

## Phase 3: Score (convert rank sum to 0-1 quality)

Normalize: `quality = 1.0 - (rank_sum - min_sum) / (max_sum - min_sum)`

This maps the best page to 1.0 and the worst to 0.0. Submit via `POST /api/contribute/quality` with `model: "rank-agg-v1"`. These compound with future model reviews via geometric mean — this is a baseline, not a ceiling.

## Phase 4: Model reviews (later, for tie-breaking)

Rank aggregation stratifies the index but can't distinguish two pages with the same structural score from the same domain tier. Model reviews break ties.

Use the existing quality work queue: `GET /api/work/quality`, send first 2000 chars to a model, score 0-1. Prioritize reviews for top-ranked pages first (highest impact on search quality).

## Implementation

One Python script: `sidecar/quality_scorer.py`

1. Fetch all pages via `GET /api/work/quality?limit=100` (paginate)
2. Compute structural score from text_content (regex)
3. Extract domain tier from URL
4. Rank on each feature, sum ranks
5. Normalize to 0-1
6. Submit quality scores via `POST /api/contribute/quality`

Estimated cost: zero. Estimated runtime: seconds.

## What this buys

Before: all 1,223 pages have quality = default. RFCs and anatomy chapters rank the same.

After: RFCs, Euclid, Milewski, and Grinstead float up. Thin Pressbooks stubs sink. Search results immediately stratified. xQuAD breadth reranking picks the best from each domain instead of a random one.
