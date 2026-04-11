"""Local embedding server for BGE-small-en-v1.5.

Replaces the HuggingFace Inference API with a local model.
Listens on port 8081, same request/response format as the HF endpoint.

    pip install sentence-transformers flask
    python embed_server.py
"""
import json
import sys
from http.server import HTTPServer, BaseHTTPRequestHandler
from sentence_transformers import SentenceTransformer

MODEL_NAME = "BAAI/bge-small-en-v1.5"
PORT = 8081

print(f"Loading {MODEL_NAME}...", flush=True)
model = SentenceTransformer(MODEL_NAME)
print(f"Model loaded. Serving on :{PORT}", flush=True)


class EmbedHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length))
        inputs = body.get("inputs") or body.get("texts", [])

        if isinstance(inputs, str):
            inputs = [inputs]
            single = True
        else:
            single = False

        embeddings = model.encode(inputs, normalize_embeddings=True).tolist()

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()

        if single:
            self.wfile.write(json.dumps(embeddings[0]).encode())
        else:
            self.wfile.write(json.dumps(embeddings).encode())

    def log_message(self, format, *args):
        # Suppress per-request logs
        pass


class ReuseHTTPServer(HTTPServer):
    allow_reuse_address = True


if __name__ == "__main__":
    server = ReuseHTTPServer(("127.0.0.1", PORT), EmbedHandler)
    print(f"Listening on 127.0.0.1:{PORT}", flush=True)
    server.serve_forever()
