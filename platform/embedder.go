package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"time"
)

const (
	EmbeddingModel = "BAAI/bge-small-en-v1.5"
	EmbeddingDim   = 384
	hfEndpoint     = "https://router.huggingface.co/hf-inference/models/" + EmbeddingModel + "/pipeline/feature-extraction"
)

// Embedder always calls the remote HuggingFace Inference API.
//
// A local sidecar (FastAPI + ONNX Runtime) used to run alongside the server
// specifically to avoid this network round-trip, but it never actually
// worked: the startup probe posted to the bare host with no path (the
// sidecar's only route is POST /embed), used a request key ("inputs") the
// sidecar's schema doesn't accept ("text"/"texts"), and Embed/EmbedBatch's
// response decoding assumed a bare JSON array (HF's shape) when the sidecar
// returned {"embedding":...,"dim":...}. Three independent, compounding
// mismatches -- useLocal was never true in production, so every embed call
// had always gone to HF regardless of the sidecar's presence. Confirmed live
// (2026-07): the sidecar consumed ~300-440MB resident for zero traffic.
// Removed rather than fixed -- HF latency was never reported as a
// bottleneck, and fixing local inference would have introduced a second,
// subtly different embedding backend (ONNX, likely quantized) alongside the
// one all 441K+ existing chunk embeddings were actually computed with.
type Embedder struct {
	httpClient *http.Client
	hfToken    string
}

func NewEmbedder() *Embedder {
	return &Embedder{
		hfToken: os.Getenv("HF_TOKEN"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
	}
}

// EmbedQuery prepends the BGE instruction prefix for retrieval queries.
// Documents should use Embed() without the prefix.
func (e *Embedder) EmbedQuery(text string) ([]float64, error) {
	return e.Embed("Represent this sentence for searching relevant passages: " + text)
}

func (e *Embedder) Embed(text string) ([]float64, error) {
	body, err := json.Marshal(map[string]string{"inputs": text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequest("POST", hfEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.hfToken)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HF API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API returned status %d", resp.StatusCode)
	}

	// HF router returns [0.1, -0.2, ...] for single input
	var result []float64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode HF response: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("HF API returned empty result")
	}

	return l2Normalize(result), nil
}

func (e *Embedder) EmbedBatch(texts []string) ([][]float64, error) {
	body, err := json.Marshal(map[string][]string{"inputs": texts})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequest("POST", hfEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.hfToken)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HF API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API returned status %d", resp.StatusCode)
	}

	// HF returns [[0.1, ...], [0.3, ...]] for batch input
	var result [][]float64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode HF response: %w", err)
	}

	for i := range result {
		result[i] = l2Normalize(result[i])
	}

	return result, nil
}

func l2Normalize(v []float64) []float64 {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return v
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}
