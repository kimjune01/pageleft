package main

import (
	"flag"
	"fmt"
	"io"
	"log"
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: pageleft <crawl|reindex|serve|chunk-backfill|link-backfill>\n")
		os.Exit(1)
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
	h := handler.New(db, embedder)

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

	log.Println("backfilling links...")
	count, err := db.BackfillLinks()
	if err != nil {
		log.Fatalf("backfill links: %v", err)
	}
	log.Printf("inserted %d links", count)

	log.Println("recomputing PageRank...")
	if err := search.ComputePageRank(db); err != nil {
		log.Fatalf("pagerank: %v", err)
	}
	log.Println("done")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
