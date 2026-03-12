package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/kimjune01/pageleft/crawler"
	"github.com/kimjune01/pageleft/handler"
	"github.com/kimjune01/pageleft/platform"
	"github.com/kimjune01/pageleft/search"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: pageleft <crawl|reindex|serve>\n")
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
