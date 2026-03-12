package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

type Embedder struct {
	baseURL    string
	httpClient *http.Client
}

func NewEmbedder(sidecarURL string) *Embedder {
	return &Embedder{
		baseURL: sidecarURL,
		httpClient: &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 128,
				IdleConnTimeout:    90 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
	}
}

type embedRequest struct {
	Text  string   `json:"text,omitempty"`
	Texts []string `json:"texts,omitempty"`
}

type embedResponse struct {
	Embedding  []float64   `json:"embedding,omitempty"`
	Embeddings [][]float64 `json:"embeddings,omitempty"`
	Dim        int         `json:"dim"`
}

func (e *Embedder) Embed(text string) ([]float64, error) {
	body, err := json.Marshal(embedRequest{Text: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	resp, err := e.httpClient.Post(e.baseURL+"/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sidecar request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sidecar returned status %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sidecar response: %w", err)
	}

	return result.Embedding, nil
}

func (e *Embedder) EmbedBatch(texts []string) ([][]float64, error) {
	body, err := json.Marshal(embedRequest{Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	resp, err := e.httpClient.Post(e.baseURL+"/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sidecar request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sidecar returned status %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sidecar response: %w", err)
	}

	return result.Embeddings, nil
}
