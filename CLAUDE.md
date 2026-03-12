# PageLeft

## Build & Deploy
- `go build ./...` to verify
- `./deploy.sh` to cross-compile, upload, restart, and smoke test

## Architecture
- Go server (HTTP + SQLite), Python sidecar for local inference (port 8081)
- HF free tier for embeddings via `platform/embedder.go`
- Embedding model declared as constants in `platform/embedder.go` (`EmbeddingModel`, `EmbeddingDim`)

## README is the API doc
When changing API behavior, endpoints, request/response shapes, or the embedding model, update `README.md` to match. The README is how federated workers and agents discover the API contract.
