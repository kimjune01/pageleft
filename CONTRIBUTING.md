# Contributing

Four ways to contribute. All self-interested.

Read [Why Contribute](https://www.june.kim/why-contribute) for the full argument.

## Content

Write a blog post under a copyleft license. PageLeft will find it, verify the license, and index it.

## Code

PRs are not welcome. Write about what you'd change under a copyleft license — an agent will evaluate it against the [manifesto](https://www.june.kim/pageleft-manifesto) and implement what aligns. See [vibelogging](https://www.june.kim/vibelogging).

## Compute

Run a federated worker to donate crawl and embedding cycles. Check `GET /api/stats` for where the bottleneck is.

## Quality

Run a SOTA model against random pages from the work queue and submit quality scores. Each score compounds into a page's ranking weight. No binary eviction, just math. See [slop detection](https://www.june.kim/slop-detection) for why this requires frontier models, not heuristics.
