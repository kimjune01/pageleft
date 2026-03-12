package platform

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
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
	CrawledAt   time.Time
	ContentHash string
}

type Link struct {
	ID         int64
	FromPageID int64
	ToPageID   int64
	AnchorText string
}

type FrontierEntry struct {
	ID           int64
	URL          string
	Depth        int
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
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
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
			discovered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_pages_url ON pages(url);
		CREATE INDEX IF NOT EXISTS idx_frontier_depth ON frontier(depth);
	`)
	return err
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
	err := db.conn.QueryRow("SELECT id, url, title, text_content, license_url, license_type, embedding, pagerank, crawled_at, content_hash FROM pages WHERE url = ?", url).
		Scan(&p.ID, &p.URL, &p.Title, &p.TextContent, &p.LicenseURL, &p.LicenseType, &embJSON, &p.PageRank, &p.CrawledAt, &p.ContentHash)
	if err != nil {
		return nil, err
	}
	if embJSON.Valid && embJSON.String != "" {
		json.Unmarshal([]byte(embJSON.String), &p.Embedding)
	}
	return p, nil
}

func (db *DB) AllPages() ([]*Page, error) {
	rows, err := db.conn.Query("SELECT id, url, title, text_content, license_url, license_type, embedding, pagerank, crawled_at, content_hash FROM pages")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pages []*Page
	for rows.Next() {
		p := &Page{}
		var embJSON sql.NullString
		if err := rows.Scan(&p.ID, &p.URL, &p.Title, &p.TextContent, &p.LicenseURL, &p.LicenseType, &embJSON, &p.PageRank, &p.CrawledAt, &p.ContentHash); err != nil {
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

func (db *DB) AddToFrontier(url string, depth int) error {
	_, err := db.conn.Exec(`
		INSERT INTO frontier (url, depth) VALUES (?, ?)
		ON CONFLICT(url) DO NOTHING`, url, depth)
	return err
}

func (db *DB) PopFrontier(limit int) ([]*FrontierEntry, error) {
	rows, err := db.conn.Query("SELECT id, url, depth, discovered_at FROM frontier ORDER BY depth ASC, id ASC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*FrontierEntry
	var ids []int64
	for rows.Next() {
		e := &FrontierEntry{}
		if err := rows.Scan(&e.ID, &e.URL, &e.Depth, &e.DiscoveredAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
		ids = append(ids, e.ID)
	}

	for _, id := range ids {
		db.conn.Exec("DELETE FROM frontier WHERE id = ?", id)
	}

	return entries, nil
}

func (db *DB) FrontierSize() (int, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM frontier").Scan(&n)
	return n, err
}

func (db *DB) IsURLKnown(url string) (bool, error) {
	var n int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM pages WHERE url = ? UNION ALL SELECT COUNT(*) FROM frontier WHERE url = ?", url, url).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
