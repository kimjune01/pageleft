---
name: burst
description: "Resize to t4g.large, run crawl/quality/embed workers, prune, then downgrade back to t4g.small."
user_invocable: true
allowed-tools: Bash, Read, Edit, Write, Monitor, TaskStop, Agent
---

# Burst: Scale up, crawl, score, prune, scale down

Run a burst contribution cycle on the PageLeft server. Temporarily upgrades
the EC2 instance for heavy crawl + embed + quality work, then downgrades.

## Constants

- **Instance ID**: `i-0b4b5c5bc2aaa35d2`
- **Server**: `ubuntu@44.245.126.104`
- **API**: `https://pageleft.cc/api`
- **Burst size**: `t4g.large` (8GB)
- **Idle size**: `t4g.small` (2GB)

## Process

### 1. Upgrade

```
aws ec2 stop-instances --instance-ids $INSTANCE_ID
aws ec2 wait instance-stopped --instance-ids $INSTANCE_ID
aws ec2 modify-instance-attribute --instance-id $INSTANCE_ID --instance-type '{"Value":"t4g.large"}'
aws ec2 start-instances --instance-ids $INSTANCE_ID
aws ec2 wait instance-status-ok --instance-ids $INSTANCE_ID
```

Verify: `curl -sf https://pageleft.cc/api/stats` and `ssh $SERVER "free -h"`.

### 2. Start workers

Run three workers in parallel as background processes. Each worker must:
- Use exponential backoff (base 2s, max 120s)
- Reset backoff on success
- Never exit — poll forever until killed

**Crawl worker**: Pull URLs from `GET /api/frontier?limit=10`, submit each
to `POST /api/contribute/page`. Log accepted/rejected with counts.

**Quality worker**: Fetch pages from `GET /api/work/quality?limit=5`, score
each with Claude Sonnet via the Anthropic API (0.0–1.0 scale), submit to
`POST /api/contribute/quality`. Use `%s` string formatting, NOT `.format()`,
because page content contains curly braces.

**Embed worker**: Fetch chunks from `GET /api/work/embed?limit=32`, embed
via `POST /api/embed`, submit to `POST /api/contribute/embeddings`.

### 3. Monitor

Periodically report stats: `curl -sf https://pageleft.cc/api/stats`.
Sample recent pages to check content quality. Watch for:
- Wikimedia satellite domains leaking through (block and prune if found)
- Thin/empty pages (< 500 bytes)
- Server load (`ssh $SERVER "uptime"`)

Let the user decide when to stop. They may want to run for minutes or hours.

### 4. Wind down

When the user says stop:

1. Kill all workers
2. Prune: `ssh $SERVER "sudo systemctl stop pageleft-server && PAGELEFT_DB=/var/lib/pageleft/pageleft.db /usr/local/bin/pageleft prune-pages && sudo systemctl start pageleft-server"`
3. Final stats check
4. Downgrade:

```
aws ec2 stop-instances --instance-ids $INSTANCE_ID
aws ec2 wait instance-stopped --instance-ids $INSTANCE_ID
aws ec2 modify-instance-attribute --instance-id $INSTANCE_ID --instance-type '{"Value":"t4g.small"}'
aws ec2 start-instances --instance-ids $INSTANCE_ID
aws ec2 wait instance-status-ok --instance-ids $INSTANCE_ID
```

5. Smoke test: `curl -sf https://pageleft.cc/api/stats`

## Notes

- Embed worker calls the public `/api/embed` endpoint, which proxies to the
  remote HuggingFace Inference API. There is no local embed sidecar — one
  used to run alongside the server, but it never actually worked (a
  three-way schema mismatch meant the local-embedder probe always failed
  silently and every embed call went to HF regardless) and was removed
  rather than fixed, since HF latency was never a reported bottleneck.
- Quality worker uses `ANTHROPIC_API_KEY` from the local environment.
- The crawl worker goes through the server API, not direct DB access.
- If server becomes unresponsive (load > 10), workers back off automatically.
  Consider killing the crawl worker to let quality/embed catch up.
