"""Embedding sidecar — wraps sentence-transformers behind a single POST /embed endpoint."""

import os

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

MODEL_NAME = os.getenv("EMBED_MODEL", "BAAI/bge-small-en-v1.5")

app = FastAPI(title="PageLeft Embedding Sidecar")
model: SentenceTransformer | None = None


@app.on_event("startup")
def load_model() -> None:
    global model
    model = SentenceTransformer(MODEL_NAME)


class SingleRequest(BaseModel):
    text: str | None = None
    texts: list[str] | None = None


class EmbeddingResponse(BaseModel):
    embedding: list[float] | None = None
    embeddings: list[list[float]] | None = None
    dim: int


@app.post("/embed", response_model=EmbeddingResponse)
def embed(req: SingleRequest) -> EmbeddingResponse:
    if req.text is None and req.texts is None:
        raise HTTPException(status_code=400, detail="provide 'text' or 'texts'")

    assert model is not None

    if req.text is not None:
        vec = model.encode(req.text, normalize_embeddings=True).tolist()
        return EmbeddingResponse(embedding=vec, dim=len(vec))

    # batch mode
    vecs = model.encode(req.texts, normalize_embeddings=True).tolist()
    return EmbeddingResponse(embeddings=vecs, dim=len(vecs[0]) if vecs else 0)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "model": MODEL_NAME}
