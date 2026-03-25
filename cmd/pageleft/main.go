package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/handler"
	"github.com/kimjune01/pageleft/platform"
	"github.com/kimjune01/pageleft/search"
)

var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: pageleft <crawl|reindex|serve|version|chunk-backfill|link-backfill|embed-backfill>\n")
		os.Exit(1)
	}

	if os.Args[1] == "version" {
		fmt.Println(Version)
		return
	}

	dbPath := envOr("PAGELEFT_DB", "pageleft.db")

	switch os.Args[1] {
	case "crawl":
		cmdCrawl(dbPath)
	case "reindex":
		cmdReindex(dbPath)
	case "serve":
		cmdServe(dbPath)
	case "chunk-backfill":
		cmdChunkBackfill(dbPath)
	case "link-backfill":
		cmdLinkBackfill(dbPath)
	case "embed-backfill":
		cmdEmbedBackfill(dbPath)
	case "prune-frontier":
		cmdPruneFrontier(dbPath)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdCrawl(dbPath string) {
	fs := flag.NewFlagSet("crawl", flag.ExitOnError)
	seeds := fs.String("seeds", "", "comma-separated seed URLs")
	maxPages := fs.Int("max-pages", 100, "maximum pages to crawl")
	fs.Parse(os.Args[2:])

	if *seeds == "" {
		log.Fatal("--seeds is required")
	}

	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	embedder := platform.NewEmbedder()
	c := crawler.New(db, embedder, *maxPages)

	seedList := strings.Split(*seeds, ",")
	for i := range seedList {
		seedList[i] = strings.TrimSpace(seedList[i])
	}

	if err := c.Crawl(seedList); err != nil {
		log.Fatalf("crawl failed: %v", err)
	}
}

func cmdReindex(dbPath string) {
	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	log.Println("computing PageRank...")
	if err := search.ComputePageRank(db); err != nil {
		log.Fatalf("pagerank failed: %v", err)
	}
	log.Println("done")
}

func cmdServe(dbPath string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.String("port", "8080", "listen port")
	fs.Parse(os.Args[2:])

	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	embedder := platform.NewEmbedder()
	h := handler.New(db, embedder, Version)

	addr := ":" + *port
	log.Printf("serving on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, h.Mux()); err != nil {
		log.Fatal(err)
	}
}

func cmdChunkBackfill(dbPath string) {
	fs := flag.NewFlagSet("chunk-backfill", flag.ExitOnError)
	batchSize := fs.Int("batch", 50, "pages per batch")
	fs.Parse(os.Args[2:])

	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	client := &http.Client{Timeout: 30 * time.Second}
	total := 0

	for {
		pages, err := db.PagesWithoutChunks(*batchSize)
		if err != nil {
			log.Fatalf("query pages: %v", err)
		}
		if len(pages) == 0 {
			break
		}

		for _, p := range pages {
			req, err := http.NewRequest("GET", p.URL, nil)
			if err != nil {
				log.Printf("skip %s: %v", p.URL, err)
				continue
			}
			req.Header.Set("User-Agent", crawler.RobotsUserAgent)

			resp, err := client.Do(req)
			if err != nil {
				log.Printf("fetch %s: %v", p.URL, err)
				continue
			}

			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				log.Printf("skip %s: status %d", p.URL, resp.StatusCode)
				continue
			}

			doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
			if err != nil {
				log.Printf("parse %s: %v", p.URL, err)
				continue
			}

			paragraphs := crawler.ExtractParagraphs(doc)
			if len(paragraphs) == 0 {
				log.Printf("no paragraphs: %s", p.URL)
				continue
			}

			chunks := make([]platform.Chunk, len(paragraphs))
			for i, text := range paragraphs {
				chunks[i] = platform.Chunk{
					PageID: p.ID,
					Idx:    i,
					Text:   text,
					// Embedding left nil — federated work queue will handle it
				}
			}
			if err := db.InsertChunks(p.ID, chunks); err != nil {
				log.Printf("insert chunks %s: %v", p.URL, err)
				continue
			}

			total++
			log.Printf("[%d] backfilled %d chunks for %s", total, len(paragraphs), p.URL)
			time.Sleep(1 * time.Second) // polite rate limiting
		}
	}

	log.Printf("chunk-backfill complete: %d pages processed", total)
}

func cmdLinkBackfill(dbPath string) {
	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	pages, err := db.AllPages()
	if err != nil {
		log.Fatalf("load pages: %v", err)
	}
	urlMap, err := db.PageURLMap()
	if err != nil {
		log.Fatalf("load url map: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	linkCount := 0

	for i, p := range pages {
		req, err := http.NewRequest("GET", p.URL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", crawler.RobotsUserAgent)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
		if err != nil {
			continue
		}

		hrefs := crawler.ExtractLinks(doc, p.URL)
		for _, href := range hrefs {
			if targetID, ok := urlMap[href]; ok && targetID != p.ID {
				if err := db.InsertLink(p.ID, targetID, ""); err == nil {
					linkCount++
				}
			}
		}

		if (i+1)%50 == 0 {
			log.Printf("processed %d/%d pages, %d links found", i+1, len(pages), linkCount)
		}
		time.Sleep(500 * time.Millisecond)
	}

	log.Printf("inserted %d links from %d pages", linkCount, len(pages))

	log.Println("recomputing PageRank...")
	if err := search.ComputePageRank(db); err != nil {
		log.Fatalf("pagerank: %v", err)
	}
	log.Println("done")
}

func cmdEmbedBackfill(dbPath string) {
	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Find pages without embeddings that have chunks with embeddings.
	rows, err := db.RawQuery(`
		SELECT p.id, p.url
		FROM pages p
		WHERE (p.embedding IS NULL OR length(p.embedding) <= 5)
		AND EXISTS (
			SELECT 1 FROM chunks c
			WHERE c.page_id = p.id
			AND c.embedding IS NOT NULL AND length(c.embedding) > 5
		)`)
	if err != nil {
		log.Fatalf("query pages: %v", err)
	}

	type pageRef struct {
		id  int64
		url string
	}
	var pages []pageRef
	for rows.Next() {
		var p pageRef
		if err := rows.Scan(&p.id, &p.url); err != nil {
			log.Fatalf("scan: %v", err)
		}
		pages = append(pages, p)
	}
	rows.Close()

	log.Printf("found %d pages needing page-level embeddings", len(pages))

	updated := 0
	for _, p := range pages {
		chunks, err := db.ChunkEmbeddingsForPage(p.id)
		if err != nil || len(chunks) == 0 {
			continue
		}

		// Average chunk embeddings
		dim := len(chunks[0])
		avg := make([]float64, dim)
		for _, emb := range chunks {
			for i, v := range emb {
				avg[i] += v
			}
		}
		n := float64(len(chunks))
		// Normalize: average then L2-normalize
		var norm float64
		for i := range avg {
			avg[i] /= n
			norm += avg[i] * avg[i]
		}
		if norm > 0 {
			norm = 1.0 / (norm * 0.5) // sqrt via newton? just use math
		}
		// Actually use math.Sqrt
		normSqrt := 0.0
		for _, v := range avg {
			normSqrt += v * v
		}
		if normSqrt > 0 {
			scale := 1.0 / math.Sqrt(normSqrt)
			for i := range avg {
				avg[i] *= scale
			}
		}

		if err := db.UpdateEmbedding(p.id, avg); err != nil {
			log.Printf("update %s: %v", p.url, err)
			continue
		}
		updated++
		if updated%50 == 0 {
			log.Printf("  %d page embeddings backfilled", updated)
		}
	}

	log.Printf("embed-backfill complete: %d pages updated", updated)
}

func cmdPruneFrontier(dbPath string) {
	db, err := platform.NewDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	before, _ := db.FrontierSize()
	log.Printf("frontier before: %d entries", before)

	removed, err := db.PruneFrontier()
	if err != nil {
		log.Fatalf("prune: %v", err)
	}

	after, _ := db.FrontierSize()
	log.Printf("pruned %d entries, frontier after: %d", removed, after)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
