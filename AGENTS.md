# PageLeft

## Build & Deploy
- `go build ./...` to verify
- `./deploy.sh` to cross-compile, upload, restart, and smoke test

## Architecture
- Go server (HTTP + SQLite), Python sidecar for local inference (port 8081)
- HF free tier for embeddings via `platform/embedder.go`
- Embedding model declared as constants in `platform/embedder.go` (`EmbeddingModel`, `EmbeddingDim`)

## Production
- EC2 t4g.micro ARM64, Caddy reverse proxy, systemd service `pageleft-server`
- DB: `/var/lib/pageleft/pageleft.db` (SQLite WAL)
- Service runs as root (no `User=` in unit file). DB is owned by root.
- Backfill commands must run with `sudo` and with the server stopped:
  ```
  sudo systemctl stop pageleft-server
  sudo PAGELEFT_DB=/var/lib/pageleft/pageleft.db /usr/local/bin/pageleft link-backfill
  sudo systemctl start pageleft-server
  ```
- `link-backfill` re-fetches every page to extract `<a>` tags, matches against known pages, inserts links, then recomputes PageRank.

## README is the API doc
When changing API behavior, endpoints, request/response shapes, or the embedding model, update `README.md` to match. The README is how federated workers and agents discover the API contract.

## Keep the roadmap current
After implementing a feature, move it from its current section in `docs/ROADMAP.md` to **Done**. If the work revealed new open problems, add them.
