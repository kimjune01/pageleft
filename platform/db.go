package platform

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn          *sql.DB
	urlBloom      *urlBloomFilter
	bloomPath     string
	chunkBloom    *urlBloomFilter
	chunkBloomPath string
}

const (
	urlBloomM = 19_200_000 // bits — optimal for 1M items at 0.01% FPR (~2.4 MB)
	urlBloomK = 13         // hash functions
)

// urlBloomFilter is a persistent probabilistic set for URL dedup.
// Sized for 1M URLs at 0.1% FPR (~1.8 MB on disk).
type urlBloomFilter struct {
	bits []uint64
	mu   sync.RWMutex
}

func newURLBloomFilter() *urlBloomFilter {
	return &urlBloomFilter{bits: make([]uint64, urlBloomWords)}
}

const urlBloomWords = (urlBloomM + 63) / 64

func (bf *urlBloomFilter) add(item string) {
	h1, h2 := urlBloomHashes(item)
	bf.mu.Lock()
	for i := 0; i < urlBloomK; i++ {
		pos := (h1 + uint64(i)*h2) % urlBloomM
		bf.bits[pos/64] |= 1 << (pos % 64)
	}
	bf.mu.Unlock()
}

func (bf *urlBloomFilter) contains(item string) bool {
	h1, h2 := urlBloomHashes(item)
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	for i := 0; i < urlBloomK; i++ {
		pos := (h1 + uint64(i)*h2) % urlBloomM
		if bf.bits[pos/64]&(1<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

func urlBloomHashes(s string) (uint64, uint64) {
	h := fnv.New64a()
	h.Write([]byte(s))
	h1 := h.Sum64()
	h2 := fnv.New64()
	h2.Write([]byte(s))
	return h1, h2.Sum64()
}

func loadURLBloom(path string) *urlBloomFilter {
	data, err := os.ReadFile(path)
	if err == nil && len(data) == urlBloomWords*8 {
		bf := newURLBloomFilter()
		for i := range bf.bits {
			bf.bits[i] = binary.LittleEndian.Uint64(data[i*8:])
		}
		return bf
	}
	return newURLBloomFilter()
}

func (bf *urlBloomFilter) save(path string) error {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	data := make([]byte, len(bf.bits)*8)
	for i, v := range bf.bits {
		binary.LittleEndian.PutUint64(data[i*8:], v)
	}
	return os.WriteFile(path, data, 0644)
}

type Page struct {
	ID          int64
	URL         string
	Title       string
	TextContent string
	LicenseURL  string
	LicenseType string
	Embedding   []float64
	PageRank    float64
	Quality     float64
	Compilable  bool
	CrawledAt   time.Time
	ContentHash string
}

type Link struct {
	ID         int64
	FromPageID int64
	ToPageID   int64
	AnchorText string
}

type Chunk struct {
	ID        int64
	PageID    int64
	Idx       int
	Text      string
	Embedding []float64
}

type ChunkWithPage struct {
	Chunk
	PageURL     string
	PageTitle   string
	PageRank    float64
	Quality     float64
	Compilable  bool
	LicenseType string
}

type FrontierEntry struct {
	ID           int64
	URL          string
	Depth        int
	Priority     float64
	DiscoveredAt time.Time
}


func NewDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	dir := filepath.Dir(path)
	bloomPath := filepath.Join(dir, "url-seen.bloom")
	chunkBloomPath := filepath.Join(dir, "chunk-seen.bloom")
	db := &DB{conn: conn, bloomPath: bloomPath, chunkBloomPath: chunkBloomPath}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	db.initURLBloom()
	db.initChunkBloom()
	return db, nil
}

// initURLBloom loads the persistent bloom filter, or seeds from DB if missing.
func (db *DB) initURLBloom() {
	db.urlBloom = loadURLBloom(db.bloomPath)
	// Check if the filter was loaded from disk (non-empty) or needs seeding.
	empty := true
	for _, w := range db.urlBloom.bits {
		if w != 0 {
			empty = false
			break
		}
	}
	if !empty {
		return // loaded from disk
	}
	// First run or missing file: seed from DB.
	for _, table := range []string{"pages", "frontier"} {
		rows, err := db.conn.Query("SELECT url FROM " + table)
		if err != nil {
			continue
		}
		for rows.Next() {
			var u string
			rows.Scan(&u)
			db.urlBloom.add(NormalizeURL(u))
		}
		rows.Close()
	}
	db.urlBloom.save(db.bloomPath)
}

// initChunkBloom loads the chunk content bloom filter, or seeds from DB if missing.
func (db *DB) initChunkBloom() {
	db.chunkBloom = loadURLBloom(db.chunkBloomPath)
	empty := true
	for _, w := range db.chunkBloom.bits {
		if w != 0 {
			empty = false
			break
		}
	}
	if !empty {
		return
	}
	rows, err := db.conn.Query("SELECT text FROM chunks")
	if err != nil {
		return
	}
	for rows.Next() {
		var t string
		rows.Scan(&t)
		db.chunkBloom.add(t)
	}
	rows.Close()
	db.chunkBloom.save(db.chunkBloomPath)
}

// SaveURLBloom persists the URL bloom filter to disk. Call periodically
// or on graceful shutdown to avoid re-seeding from DB on next startup.
func (db *DB) SaveURLBloom() error {
	if db.urlBloom == nil {
		return nil
	}
	return db.urlBloom.save(db.bloomPath)
}

// WALCheckpoint merges the write-ahead log into the main DB file.
// Uses PASSIVE mode: non-blocking, merges what it can without waiting.
func (db *DB) WALCheckpoint() {
	db.conn.Exec("PRAGMA wal_checkpoint(PASSIVE)")
}

func (db *DB) Close() error {
	db.SaveURLBloom()
	if db.chunkBloom != nil {
		db.chunkBloom.save(db.chunkBloomPath)
	}
	return db.conn.Close()
}

func (db *DB) migrate() error {
	if _, err := db.conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			text_content TEXT NOT NULL DEFAULT '',
			license_url TEXT NOT NULL DEFAULT '',
			license_type TEXT NOT NULL DEFAULT '',
			embedding JSON,
			pagerank REAL NOT NULL DEFAULT 0,
			crawled_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			content_hash TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			from_page_id INTEGER NOT NULL REFERENCES pages(id),
			to_page_id INTEGER NOT NULL REFERENCES pages(id),
			anchor_text TEXT NOT NULL DEFAULT '',
			UNIQUE(from_page_id, to_page_id)
		);
		CREATE TABLE IF NOT EXISTS frontier (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE NOT NULL,
			depth INTEGER NOT NULL DEFAULT 0,
			inbound INTEGER NOT NULL DEFAULT 1,
			discovered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			idx INTEGER NOT NULL,
			text TEXT NOT NULL DEFAULT '',
			embedding JSON,
			UNIQUE(page_id, idx)
		);
		CREATE TABLE IF NOT EXISTS contributions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			contributor TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_contributions_type ON contributions(type);
		CREATE TABLE IF NOT EXISTS quality_reviews (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			score REAL NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			contributor TEXT NOT NULL DEFAULT '',
			reviewed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_pages_url ON pages(url);
		CREATE INDEX IF NOT EXISTS idx_frontier_depth ON frontier(depth);
		CREATE INDEX IF NOT EXISTS idx_chunks_page_id ON chunks(page_id);
		CREATE INDEX IF NOT EXISTS idx_quality_reviews_page_id ON quality_reviews(page_id);
	`)
	if err != nil {
		return err
	}
	// Add columns (idempotent — ignore error if already exists)
	db.conn.Exec("ALTER TABLE pages ADD COLUMN quality REAL NOT NULL DEFAULT 1.0")
	db.conn.Exec("ALTER TABLE pages ADD COLUMN compilable INTEGER NOT NULL DEFAULT 0")
	db.conn.Exec("ALTER TABLE quality_reviews ADD COLUMN contributor TEXT NOT NULL DEFAULT ''")
	db.conn.Exec("ALTER TABLE frontier ADD COLUMN inbound INTEGER NOT NULL DEFAULT 1")
	db.conn.Exec("CREATE INDEX IF NOT EXISTS idx_frontier_inbound ON frontier(inbound DESC)")
	return nil
}

// --- Pages ---

func (db *DB) InsertPage(p *Page) (int64, error) {
	embJSON, err := json.Marshal(p.Embedding)
	if err != nil {
		return 0, fmt.Errorf("marshal embedding: %w", err)
	}
	res, err := db.conn.Exec(`
		INSERT INTO pages (url, title, text_content, license_url, license_type, embedding, crawled_at, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			title=excluded.title, text_content=excluded.text_content,
			license_url=excluded.license_url, license_type=excluded.license_type,
			embedding=excluded.embedding, crawled_at=excluded.crawled_at,
			content_hash=excluded.content_hash`,
		p.URL, p.Title, p.TextContent, p.LicenseURL, p.LicenseType, string(embJSON), p.CrawledAt, p.ContentHash)
	if err != nil {
		return 0, fmt.Errorf("insert page: %w", err)
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// upsert case — look up existing ID
		row := db.conn.QueryRow("SELECT id FROM pages WHERE url = ?", p.URL)
		row.Scan(&id)
	}
	return id, nil
}

func (db *DB) GetPageByURL(url string) (*Page, error) {
	p := &Page{}
	var embJSON sql.NullString
	err := db.conn.QueryRow("SELECT id, url, title, text_content, license_url, license_type, embedding, pagerank, quality, compilable, crawled_at, content_hash FROM pages WHERE url = ?", url).
		Scan(&p.ID, &p.URL, &p.Title, &p.TextContent, &p.LicenseURL, &p.LicenseType, &embJSON, &p.PageRank, &p.Quality, &p.Compilable, &p.CrawledAt, &p.ContentHash)
	if err != nil {
		return nil, err
	}
	if embJSON.Valid && embJSON.String != "" {
		json.Unmarshal([]byte(embJSON.String), &p.Embedding)
	}
	return p, nil
}

func (db *DB) AllPages() ([]*Page, error) {
	rows, err := db.conn.Query("SELECT id, url, title, text_content, license_url, license_type, embedding, pagerank, quality, compilable, crawled_at, content_hash FROM pages")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pages []*Page
	for rows.Next() {
		p := &Page{}
		var embJSON sql.NullString
		if err := rows.Scan(&p.ID, &p.URL, &p.Title, &p.TextContent, &p.LicenseURL, &p.LicenseType, &embJSON, &p.PageRank, &p.Quality, &p.Compilable, &p.CrawledAt, &p.ContentHash); err != nil {
			return nil, err
		}
		if embJSON.Valid && embJSON.String != "" {
			json.Unmarshal([]byte(embJSON.String), &p.Embedding)
		}
		pages = append(pages, p)
	}
	return pages, nil
}

func (db *DB) UpdatePageRank(id int64, rank float64) error {
	_, err := db.conn.Exec("UPDATE pages SET pagerank = ? WHERE id = ?", rank, id)
	return err
}

func (db *DB) UpdateEmbedding(id int64, emb []float64) error {
	embJSON, err := json.Marshal(emb)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec("UPDATE pages SET embedding = ? WHERE id = ?", string(embJSON), id)
	return err
}

// RawQuery exposes db.conn.Query for ad-hoc queries (backfill commands).
func (db *DB) RawQuery(query string, args ...any) (*sql.Rows, error) {
	return db.conn.Query(query, args...)
}

// ChunkEmbeddingsForPage returns all non-null chunk embeddings for a page.
func (db *DB) ChunkEmbeddingsForPage(pageID int64) ([][]float64, error) {
	rows, err := db.conn.Query(`
		SELECT embedding FROM chunks
		WHERE page_id = ? AND embedding IS NOT NULL AND length(embedding) > 5`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var embeddings [][]float64
	for rows.Next() {
		var embJSON string
		if err := rows.Scan(&embJSON); err != nil {
			return nil, err
		}
		var emb []float64
		if err := json.Unmarshal([]byte(embJSON), &emb); err != nil {
			continue
		}
		if len(emb) > 0 {
			embeddings = append(embeddings, emb)
		}
	}
	return embeddings, nil
}

func (db *DB) PageCount() (int, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM pages").Scan(&n)
	return n, err
}

// --- Links ---

func (db *DB) InsertLink(fromID, toID int64, anchor string) error {
	_, err := db.conn.Exec(`
		INSERT INTO links (from_page_id, to_page_id, anchor_text)
		VALUES (?, ?, ?)
		ON CONFLICT(from_page_id, to_page_id) DO NOTHING`,
		fromID, toID, anchor)
	return err
}

func (db *DB) AllLinks() ([]*Link, error) {
	rows, err := db.conn.Query("SELECT id, from_page_id, to_page_id, anchor_text FROM links")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []*Link
	for rows.Next() {
		l := &Link{}
		if err := rows.Scan(&l.ID, &l.FromPageID, &l.ToPageID, &l.AnchorText); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, nil
}

func (db *DB) LinkCount() (int, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM links").Scan(&n)
	return n, err
}

// --- Frontier ---

// AddToFrontier adds a URL to the crawl frontier if it passes basic filters.
// Each duplicate sighting increments the inbound count. Priority is computed
// on read as log(1 + inbound) * (1 + noise) — URLs linked from many indexed
// pages rise; stochastic noise shuffles within tiers.
func (db *DB) AddToFrontier(rawURL string, depth int) error {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil
	}
	normalized := NormalizeURL(rawURL)
	if normalized == "" {
		return nil
	}
	// Bloom filter: fast probabilistic check avoids DB round-trip.
	// False positives cause us to skip a URL we haven't seen — acceptable
	// at 0.1% FPR. False negatives are impossible.
	if db.urlBloom.contains(normalized) {
		return nil
	}
	db.urlBloom.add(normalized)
	_, err := db.conn.Exec(`
		INSERT INTO frontier (url, depth, inbound) VALUES (?, ?, 1)
		ON CONFLICT(url) DO UPDATE SET inbound = inbound + 1`,
		normalized, depth)
	return err
}

// NormalizeURL canonicalizes a URL: strips fragment, trailing slash,
// upgrades http to https, and collapses forge URLs to owner/repo.
func NormalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	// Canonical scheme: treat http and https as the same page.
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	// Collapse GitHub/Codeberg deep paths to owner/repo.
	// github.com/owner/repo/blob/main/file.py → github.com/owner/repo
	host := strings.ToLower(u.Hostname())
	if host == "github.com" || host == "codeberg.org" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			u.Path = "/" + parts[0] + "/" + parts[1]
		}
	}
	return u.String()
}

func (db *DB) PopFrontier(limit int) ([]*FrontierEntry, error) {
	entries, err := db.scoredFrontier(limit)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		db.conn.Exec("DELETE FROM frontier WHERE id = ?", e.ID)
	}
	return entries, nil
}

func (db *DB) FrontierSize() (int, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM frontier").Scan(&n)
	return n, err
}

// PruneFrontier removes frontier entries that are already indexed,
// non-HTTP, or matched by the provided filter function.
// Returns the number of entries removed.
func (db *DB) PruneFrontier(shouldBlock URLFilter) (int64, error) {
	// Delete already-indexed
	r1, _ := db.conn.Exec(`DELETE FROM frontier WHERE url IN (SELECT url FROM pages)`)
	n1, _ := r1.RowsAffected()

	// Delete non-HTTP
	r2, _ := db.conn.Exec(`DELETE FROM frontier WHERE url NOT LIKE 'http://%' AND url NOT LIKE 'https://%'`)
	n2, _ := r2.RowsAffected()

	// Delete domain-blocked entries
	if shouldBlock == nil {
		return n1 + n2, nil
	}
	rows, err := db.conn.Query("SELECT id, url FROM frontier")
	if err != nil {
		return n1 + n2, err
	}
	var toDelete []int64
	for rows.Next() {
		var id int64
		var u string
		rows.Scan(&id, &u)
		if shouldBlock(u) {
			toDelete = append(toDelete, id)
		}
	}
	rows.Close()

	for _, id := range toDelete {
		db.conn.Exec("DELETE FROM frontier WHERE id = ?", id)
	}

	return n1 + n2 + int64(len(toDelete)), nil
}

// PeekFrontier returns frontier entries without removing them.
func (db *DB) PeekFrontier(limit int) ([]*FrontierEntry, error) {
	return db.scoredFrontier(limit)
}

// DeleteFrontierURL removes a URL from the frontier (e.g., after rejection).
func (db *DB) DeleteFrontierURL(rawURL string) {
	db.conn.Exec("DELETE FROM frontier WHERE url = ?", rawURL)
}

// PrunePages deletes pages whose URLs match the provided filter, along with
// their chunks, links, and quality reviews. Returns the number of pages removed.
// Used to retroactively purge content that should never have been indexed.
func (db *DB) PrunePages(shouldDelete URLFilter) (int64, error) {
	if shouldDelete == nil {
		return 0, nil
	}

	rows, err := db.conn.Query("SELECT id, url FROM pages")
	if err != nil {
		return 0, err
	}
	var toDelete []int64
	for rows.Next() {
		var id int64
		var u string
		rows.Scan(&id, &u)
		if shouldDelete(u) {
			toDelete = append(toDelete, id)
		}
	}
	rows.Close()

	for _, id := range toDelete {
		db.conn.Exec("DELETE FROM chunks WHERE page_id = ?", id)
		db.conn.Exec("DELETE FROM links WHERE from_page_id = ? OR to_page_id = ?", id, id)
		db.conn.Exec("DELETE FROM quality_reviews WHERE page_id = ?", id)
		db.conn.Exec("DELETE FROM pages WHERE id = ?", id)
	}

	return int64(len(toDelete)), nil
}

// scoredFrontier fetches frontier entries, overfetches 3x, scores with
// log(1 + inbound) * (1 + noise), sorts, and returns top `limit`.
// The noise term shuffles entries within the same inbound tier,
// giving exploration without overriding exploitation.
func (db *DB) scoredFrontier(limit int) ([]*FrontierEntry, error) {
	fetch := limit * 3
	if fetch < 100 {
		fetch = 100
	}
	rows, err := db.conn.Query(
		"SELECT id, url, depth, inbound, discovered_at FROM frontier ORDER BY inbound DESC LIMIT ?", fetch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pool []*FrontierEntry
	for rows.Next() {
		e := &FrontierEntry{}
		var inbound int
		if err := rows.Scan(&e.ID, &e.URL, &e.Depth, &inbound, &e.DiscoveredAt); err != nil {
			return nil, err
		}
		noise := 1.0 + rand.Float64()*0.1
		e.Priority = math.Log(1+float64(inbound)) * noise
		pool = append(pool, e)
	}

	sort.Slice(pool, func(i, j int) bool {
		return pool[i].Priority > pool[j].Priority
	})

	if len(pool) > limit {
		pool = pool[:limit]
	}
	return pool, nil
}


// URLFilter returns true if a URL should be excluded from the frontier.
type URLFilter func(string) bool

// InsertPageWithLinks inserts a page and its outgoing links in one call.
// Outbound links go to the frontier for discovery, filtered by the provided function.
func (db *DB) InsertPageWithLinks(p *Page, links []string, shouldBlock URLFilter) (int64, error) {
	pageID, err := db.InsertPage(p)
	if err != nil {
		return 0, err
	}

	for _, targetURL := range links {
		target, _ := db.GetPageByURL(targetURL)
		if target != nil {
			db.InsertLink(pageID, target.ID, "")
		}
		if shouldBlock != nil && shouldBlock(targetURL) {
			continue
		}
		db.AddToFrontier(targetURL, 0)
	}

	return pageID, nil
}

// --- Chunks ---

func (db *DB) InsertChunks(pageID int64, chunks []Chunk) error {
	for _, c := range chunks {
		// Skip chunks whose text already exists somewhere in the corpus.
		if db.chunkBloom != nil && db.chunkBloom.contains(c.Text) {
			continue
		}
		if db.chunkBloom != nil {
			db.chunkBloom.add(c.Text)
		}
		var embJSON *string
		if len(c.Embedding) > 0 {
			b, err := json.Marshal(c.Embedding)
			if err != nil {
				return fmt.Errorf("marshal chunk embedding: %w", err)
			}
			s := string(b)
			embJSON = &s
		}
		_, err := db.conn.Exec(`
			INSERT INTO chunks (page_id, idx, text, embedding)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(page_id, idx) DO UPDATE SET text=excluded.text, embedding=excluded.embedding`,
			pageID, c.Idx, c.Text, embJSON)
		if err != nil {
			return fmt.Errorf("insert chunk: %w", err)
		}
	}
	return nil
}

func (db *DB) AllChunksWithPages() ([]ChunkWithPage, error) {
	rows, err := db.conn.Query(`
		SELECT c.id, c.page_id, c.idx, c.text, c.embedding,
		       p.url, p.title, p.pagerank, p.quality, p.compilable, p.license_type
		FROM chunks c
		JOIN pages p ON p.id = c.page_id
		WHERE c.embedding IS NOT NULL AND c.embedding != '[]' AND c.embedding != 'null'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChunkWithPage
	for rows.Next() {
		var cw ChunkWithPage
		var embJSON sql.NullString
		if err := rows.Scan(&cw.ID, &cw.PageID, &cw.Idx, &cw.Text, &embJSON,
			&cw.PageURL, &cw.PageTitle, &cw.PageRank, &cw.Quality, &cw.Compilable, &cw.LicenseType); err != nil {
			return nil, err
		}
		if embJSON.Valid && embJSON.String != "" {
			json.Unmarshal([]byte(embJSON.String), &cw.Embedding)
		}
		out = append(out, cw)
	}
	return out, nil
}

func (db *DB) ChunksWithoutEmbeddings(limit int) ([]ChunkWithPage, error) {
	rows, err := db.conn.Query(`
		SELECT c.id, c.page_id, c.idx, c.text, p.url, p.title, p.pagerank, p.license_type
		FROM chunks c
		JOIN pages p ON p.id = c.page_id
		WHERE c.embedding IS NULL OR c.embedding = '[]' OR c.embedding = 'null'
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChunkWithPage
	for rows.Next() {
		var cw ChunkWithPage
		if err := rows.Scan(&cw.ID, &cw.PageID, &cw.Idx, &cw.Text,
			&cw.PageURL, &cw.PageTitle, &cw.PageRank, &cw.LicenseType); err != nil {
			return nil, err
		}
		out = append(out, cw)
	}
	return out, nil
}

func (db *DB) UpdateChunkEmbedding(chunkID int64, emb []float64) error {
	embJSON, err := json.Marshal(emb)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec("UPDATE chunks SET embedding = ? WHERE id = ?", string(embJSON), chunkID)
	return err
}

// PageIDForChunk returns the page_id for a given chunk.
func (db *DB) PageIDForChunk(chunkID int64) (int64, error) {
	var pageID int64
	err := db.conn.QueryRow("SELECT page_id FROM chunks WHERE id = ?", chunkID).Scan(&pageID)
	return pageID, err
}

// AllChunksEmbedded returns true if every chunk for the page has an embedding.
func (db *DB) AllChunksEmbedded(pageID int64) (bool, error) {
	var missing int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM chunks
		WHERE page_id = ? AND (embedding IS NULL OR length(embedding) <= 5)`, pageID).Scan(&missing)
	return missing == 0, err
}

func (db *DB) PagesWithoutChunks(limit int) ([]*Page, error) {
	rows, err := db.conn.Query(`
		SELECT p.id, p.url, p.title, p.text_content, p.license_url, p.license_type, p.pagerank, p.crawled_at, p.content_hash
		FROM pages p
		LEFT JOIN chunks c ON c.page_id = p.id
		WHERE c.id IS NULL
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pages []*Page
	for rows.Next() {
		p := &Page{}
		if err := rows.Scan(&p.ID, &p.URL, &p.Title, &p.TextContent, &p.LicenseURL, &p.LicenseType, &p.PageRank, &p.CrawledAt, &p.ContentHash); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, nil
}

func (db *DB) ChunkCount() (int, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&n)
	return n, err
}

// --- Quality ---

// RandomPagesForReview returns random pages for quality review.
func (db *DB) RandomPagesForReview(limit int) ([]*Page, error) {
	rows, err := db.conn.Query(`
		SELECT id, url, title, text_content, license_type, quality
		FROM pages ORDER BY RANDOM() LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pages []*Page
	for rows.Next() {
		p := &Page{}
		if err := rows.Scan(&p.ID, &p.URL, &p.Title, &p.TextContent, &p.LicenseType, &p.Quality); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, nil
}

// ContributorHash returns a truncated SHA-256 of the IP for anonymous fingerprinting.
func ContributorHash(ip string) string {
	h := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(h[:8])
}

// SubmitQualityScore records a review and compounds the page's quality score.
// Same contributor can only review a given page once.
func (db *DB) SubmitQualityScore(pageID int64, score float64, model string, contributor string) error {
	var exists int
	db.conn.QueryRow(`SELECT COUNT(*) FROM quality_reviews WHERE page_id = ? AND contributor = ?`, pageID, contributor).Scan(&exists)
	if exists > 0 {
		return fmt.Errorf("already reviewed")
	}
	_, err := db.conn.Exec(`
		INSERT INTO quality_reviews (page_id, score, model, contributor) VALUES (?, ?, ?, ?)`,
		pageID, score, model, contributor)
	if err != nil {
		return fmt.Errorf("insert review: %w", err)
	}
	// Geometric mean: quality = (product of all scores) ^ (1/n)
	// Recompute from all reviews. N is small (single digits per page).
	rows, err := db.conn.Query(`SELECT score FROM quality_reviews WHERE page_id = ?`, pageID)
	if err != nil {
		return fmt.Errorf("fetch reviews: %w", err)
	}
	defer rows.Close()

	product := 1.0
	n := 0
	for rows.Next() {
		var s float64
		if err := rows.Scan(&s); err != nil {
			return fmt.Errorf("scan score: %w", err)
		}
		product *= s
		n++
	}
	if n == 0 {
		return nil
	}
	geoMean := math.Pow(product, 1.0/float64(n))
	_, err = db.conn.Exec(`UPDATE pages SET quality = ? WHERE id = ?`, geoMean, pageID)
	return err
}

// LogContribution records an anonymous contribution.
func (db *DB) LogContribution(ctype string, contributor string) {
	db.conn.Exec(`INSERT INTO contributions (type, contributor) VALUES (?, ?)`, ctype, contributor)
}

type ContributorStat struct {
	Contributor string `json:"contributor"`
	Count       int    `json:"count"`
}

// ContributorStats returns top contributors, optionally filtered by type.
func (db *DB) ContributorStats(ctype string, limit int) ([]ContributorStat, error) {
	var rows *sql.Rows
	var err error
	if ctype != "" {
		rows, err = db.conn.Query(`
			SELECT contributor, COUNT(*) as n
			FROM contributions WHERE contributor != '' AND type = ?
			GROUP BY contributor ORDER BY n DESC LIMIT ?`, ctype, limit)
	} else {
		rows, err = db.conn.Query(`
			SELECT contributor, COUNT(*) as n
			FROM contributions WHERE contributor != ''
			GROUP BY contributor ORDER BY n DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []ContributorStat
	for rows.Next() {
		var s ContributorStat
		if err := rows.Scan(&s.Contributor, &s.Count); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// QualityCoverage returns the fraction of pages with at least minReviews reviews.
func (db *DB) QualityCoverage(minReviews int) (float64, error) {
	total, err := db.PageCount()
	if err != nil || total == 0 {
		return 0, err
	}
	var reviewed int
	err = db.conn.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT page_id FROM quality_reviews
			GROUP BY page_id HAVING COUNT(*) >= ?
		)`, minReviews).Scan(&reviewed)
	if err != nil {
		return 0, nil
	}
	return float64(reviewed) / float64(total), nil
}

// PageURLMap returns a map of URL -> page ID for all pages.
func (db *DB) PageURLMap() (map[string]int64, error) {
	rows, err := db.conn.Query("SELECT id, url FROM pages")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]int64)
	for rows.Next() {
		var id int64
		var u string
		if err := rows.Scan(&id, &u); err != nil {
			return nil, err
		}
		m[u] = id
	}
	return m, nil
}

// SetCompilable marks a page as having a compilable spec or reference implementation.
func (db *DB) SetCompilable(pageID int64, compilable bool) error {
	v := 0
	if compilable {
		v = 1
	}
	_, err := db.conn.Exec("UPDATE pages SET compilable = ? WHERE id = ?", v, pageID)
	return err
}

func (db *DB) IsURLKnown(url string) (bool, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM pages WHERE url = ? UNION ALL SELECT COUNT(*) FROM frontier WHERE url = ?", url, url).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
