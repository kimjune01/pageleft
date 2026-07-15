// zenodo-drip harvests copyleft and public-domain publications from Zenodo
// and contributes them to a pageleft server, one record at a time.
//
// All heavy compute stays on the worker: the Zenodo search, the PDF download,
// and the text extraction happen here. The server only re-fetches the record's
// landing page to verify the license, then stores the worker-supplied full
// text. Run it from any machine:
//
//	go run ./cmd/zenodo-drip -api https://pageleft.cc -interval 10s
//
// Progress is checkpointed in a state file (one record URL per line), so the
// drip can be stopped and resumed freely.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

const userAgent = "pageleft-zenodo-drip/1.0 (+https://pageleft.cc)"

// maxPDFBytes skips outlier files (scanned books, supplement bundles).
const maxPDFBytes = 30 << 20

// maxTextBytes keeps the submission JSON under the server's 5 MiB body limit,
// with headroom for JSON escaping. ~3 MiB of text is over a thousand pages;
// anything longer is truncated at a page boundary.
const maxTextBytes = 3 << 20

// extractTimeout bounds PDF text extraction. Some PDFs (malformed font or
// content-stream tables) make ledongthuc/pdf loop indefinitely — observed
// firsthand on a real Zenodo record (id 3478412), which hung 30s+ with no
// CPU-bound progress. A bad PDF must not be able to stall the whole drip.
const extractTimeout = 20 * time.Second

// zenodoLicenses maps Zenodo license IDs to pageleft license info.
// Only composable copyleft and public domain licenses — mirrors
// crawler/license.go and crawler/forge.go.
// Keyed by what metadata.license.id actually comes back as on a record,
// which is NOT always what the search query's rights.id filter accepts.
// Verified empirically: cc-by-sa-4.0/3.0 round-trip identically, but a
// "cc0-1.0" search filter returns records whose license.id field is the
// older "cc-zero" — Zenodo's query and response vocabularies disagree for
// CC0 specifically. Without this entry every CC0 record was rejected as
// an unrecognized license (100% skip rate, silently, for this bucket).
var zenodoLicenses = map[string]struct{ Type, URL string }{
	"cc-by-sa-4.0": {"CC BY-SA", "https://creativecommons.org/licenses/by-sa/4.0/"},
	"cc-by-sa-3.0": {"CC BY-SA", "https://creativecommons.org/licenses/by-sa/3.0/"},
	"cc0-1.0":      {"CC0", "https://creativecommons.org/publicdomain/zero/1.0/"},
	"cc-zero":      {"CC0", "https://creativecommons.org/publicdomain/zero/1.0/"},
}

type zenodoFile struct {
	Key   string `json:"key"`
	Size  int64  `json:"size"`
	Links struct {
		Self string `json:"self"`
	} `json:"links"`
}

type zenodoRecord struct {
	ID       int64 `json:"id"`
	Metadata struct {
		Title   string `json:"title"`
		License struct {
			ID string `json:"id"`
		} `json:"license"`
		AccessRight string `json:"access_right"`
	} `json:"metadata"`
	Files []zenodoFile `json:"files"`
}

// submission mirrors handler.pageSubmission.
type submission struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	TextContent string `json:"text_content"`
	LicenseURL  string `json:"license_url"`
	LicenseType string `json:"license_type"`
}

type config struct {
	API       string        // pageleft server base URL
	ZenodoAPI string        // Zenodo base URL (overridable for tests)
	State     string        // checkpoint file path
	Licenses  []string      // Zenodo license IDs to harvest
	YearFrom  int           // first created-year bucket
	YearTo    int           // last created-year bucket
	Interval  time.Duration // pause between submissions (Zenodo crawl-delay is 10s)
	Max       int           // stop after this many submissions (0 = unlimited)
	DryRun    bool
}

func main() {
	var cfg config
	var licenses string
	flag.StringVar(&cfg.API, "api", "https://pageleft.cc", "pageleft server base URL")
	flag.StringVar(&cfg.ZenodoAPI, "zenodo-api", "https://zenodo.org", "Zenodo base URL")
	flag.StringVar(&cfg.State, "state", "zenodo-drip.state", "checkpoint file (one record URL per line)")
	flag.StringVar(&licenses, "licenses", "cc-by-sa-4.0,cc-by-sa-3.0,cc0-1.0", "comma-separated Zenodo license IDs")
	flag.IntVar(&cfg.YearFrom, "year-from", 2013, "first created-year bucket")
	flag.IntVar(&cfg.YearTo, "year-to", time.Now().Year(), "last created-year bucket")
	flag.DurationVar(&cfg.Interval, "interval", 10*time.Second, "pause between submissions")
	flag.IntVar(&cfg.Max, "max", 0, "stop after N submissions (0 = unlimited)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "list records without submitting")
	flag.Parse()

	for _, l := range strings.Split(licenses, ",") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if _, ok := zenodoLicenses[l]; !ok {
			log.Fatalf("unknown license ID %q — add it to zenodoLicenses if it is copyleft", l)
		}
		cfg.Licenses = append(cfg.Licenses, l)
	}

	n, err := run(cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("done: %d records submitted", n)
}

var client = &http.Client{Timeout: 60 * time.Second}

// run walks license × year buckets, paginating each under Zenodo's 10k
// search window, and drips every unseen record into the pageleft server.
// Returns the number of records submitted.
func run(cfg config) (int, error) {
	done, err := loadState(cfg.State)
	if err != nil {
		return 0, fmt.Errorf("load state: %w", err)
	}

	submitted := 0
	for _, lic := range cfg.Licenses {
		for year := cfg.YearFrom; year <= cfg.YearTo; year++ {
			// filetype:pdf is a real precision filter, not just an
			// optimization: verified empirically that roughly half of
			// Zenodo's CC0 "publication" records have no PDF at all (many
			// are test/placeholder deposits — e.g. record 13807, whose
			// title and description are Faker-generated Latin filler).
			// Filtering here means we never even page through those, and
			// what we do page through is denser with real hits.
			q := fmt.Sprintf(
				`metadata.rights.id:"%s" AND metadata.resource_type.id:publication* AND filetype:pdf AND created:[%d-01-01 TO %d-12-31]`,
				lic, year, year)
			for page := 1; ; page++ {
				records, err := searchPage(cfg.ZenodoAPI, q, page)
				if err != nil {
					return submitted, fmt.Errorf("search %s %d p%d: %w", lic, year, page, err)
				}
				if len(records) == 0 {
					break
				}
				for _, rec := range records {
					recordURL := fmt.Sprintf("https://zenodo.org/records/%d", rec.ID)
					if done[recordURL] {
						continue
					}
					ok, err := processRecord(cfg, rec, recordURL)
					if err != nil {
						// Transient (network, 5xx): leave out of state, retry next run.
						log.Printf("  transient failure %s: %v", recordURL, err)
						continue
					}
					if cfg.DryRun {
						// Listing only — never mutate state.
						if ok {
							submitted++
							if cfg.Max > 0 && submitted >= cfg.Max {
								return submitted, nil
							}
						}
						continue
					}
					// Success or permanent skip — either way, never revisit.
					if err := appendState(cfg.State, recordURL); err != nil {
						return submitted, fmt.Errorf("append state: %w", err)
					}
					done[recordURL] = true
					if !ok {
						continue
					}
					submitted++
					if cfg.Max > 0 && submitted >= cfg.Max {
						return submitted, nil
					}
					time.Sleep(cfg.Interval)
				}
			}
		}
	}
	return submitted, nil
}

// processRecord downloads and extracts one record's PDF, then submits it.
// Returns (false, nil) for permanent skips (no PDF, closed access, license
// rejected server-side) and (false, err) for transient failures worth retrying.
func processRecord(cfg config, rec zenodoRecord, recordURL string) (bool, error) {
	if rec.Metadata.AccessRight != "open" {
		log.Printf("  skip %s: access_right=%s", recordURL, rec.Metadata.AccessRight)
		return false, nil
	}
	lic, ok := zenodoLicenses[rec.Metadata.License.ID]
	if !ok {
		log.Printf("  skip %s: license=%s", recordURL, rec.Metadata.License.ID)
		return false, nil
	}
	file, ok := pickPDF(rec.Files, maxPDFBytes)
	if !ok {
		log.Printf("  skip %s: no usable PDF", recordURL)
		return false, nil
	}

	if cfg.DryRun {
		log.Printf("  would submit %s (%s, %.1f MB): %s",
			recordURL, file.Key, float64(file.Size)/1e6, rec.Metadata.Title)
		return true, nil
	}

	pdfBytes, err := download(file.Links.Self)
	if err != nil {
		return false, fmt.Errorf("download %s: %w", file.Key, err)
	}
	text, err := extractPDFTextTimeout(pdfBytes, extractTimeout)
	if err != nil || strings.TrimSpace(text) == "" {
		// No extractable text layer (scanned PDF), or extraction hung on a
		// pathological font/content stream. Permanent either way — OCR and
		// PDF-library debugging are both out of scope for this worker.
		log.Printf("  skip %s: no text layer (%v)", recordURL, err)
		return false, nil
	}
	if len(text) > maxTextBytes {
		cut := strings.LastIndexByte(text[:maxTextBytes], '\n')
		if cut < 0 {
			cut = maxTextBytes
		}
		log.Printf("  truncating %s: %d -> %d bytes", recordURL, len(text), cut)
		text = text[:cut]
	}

	sub := submission{
		URL:         recordURL,
		Title:       rec.Metadata.Title,
		TextContent: text,
		LicenseURL:  lic.URL,
		LicenseType: lic.Type,
	}
	status, body, err := post(cfg.API+"/api/contribute/page", sub)
	if err != nil {
		return false, err
	}
	switch {
	case status == http.StatusOK:
		log.Printf("  submitted %s: %s", recordURL, rec.Metadata.Title)
		return true, nil
	case status == http.StatusUnprocessableEntity, status == http.StatusBadRequest:
		// License rejected (422) or payload deterministically malformed (400).
		// Retrying an unchanged submission cannot succeed. Permanent.
		log.Printf("  rejected %s (status %d): %s", recordURL, status, strings.TrimSpace(body))
		return false, nil
	default:
		return false, fmt.Errorf("server status %d: %s", status, strings.TrimSpace(body))
	}
}

func searchPage(base, q string, page int) ([]zenodoRecord, error) {
	// Unauthenticated requests are capped at size=25.
	u := fmt.Sprintf("%s/api/records?size=25&page=%d&q=%s", base, page, url.QueryEscape(q))
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Past the search window, Zenodo returns 400 — end of bucket. A 400 on
	// page 1 is a real error (bad query, size cap), never end-of-results.
	if resp.StatusCode == http.StatusBadRequest && page > 1 {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Hits struct {
			Hits []zenodoRecord `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Hits.Hits, nil
}

// pickPDF returns the largest PDF file within the size cap — the main
// document rather than a supplement, when both are present.
func pickPDF(files []zenodoFile, maxBytes int64) (zenodoFile, bool) {
	var best zenodoFile
	found := false
	for _, f := range files {
		if !strings.HasSuffix(strings.ToLower(f.Key), ".pdf") {
			continue
		}
		if f.Size > maxBytes {
			continue
		}
		if !found || f.Size > best.Size {
			best, found = f, true
		}
	}
	return best, found
}

func download(fileURL string) ([]byte, error) {
	req, _ := http.NewRequest("GET", fileURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	// Read one byte past the cap so an oversized body (metadata lied about
	// the size) fails loudly instead of being silently truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPDFBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPDFBytes {
		return nil, fmt.Errorf("body exceeds %d bytes", maxPDFBytes)
	}
	return data, nil
}

// extractPDFTextTimeout runs extractPDFText on a goroutine and bails out
// after d. The goroutine is leaked (the pdf library gives no way to cancel
// mid-parse) but is bounded by process lifetime, and the caller treats a
// timeout as a permanent skip so it never retries the same record.
func extractPDFTextTimeout(data []byte, d time.Duration) (string, error) {
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := extractPDFText(data)
		done <- result{text, err}
	}()
	select {
	case r := <-done:
		return r.text, r.err
	case <-time.After(d):
		return "", fmt.Errorf("extraction exceeded %s", d)
	}
}

// extractPDFText pulls the text layer out of a PDF, one line per page,
// matching the paragraph shape the server chunks on (SplitTextContent).
func extractPDFText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var pages []string
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
		if text != "" {
			pages = append(pages, text)
		}
	}
	return strings.Join(pages, "\n"), nil
}

func post(endpoint string, sub submission) (int, string, error) {
	body, err := json.Marshal(sub)
	if err != nil {
		return 0, "", err
	}
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(respBody), nil
}

func loadState(path string) (map[string]bool, error) {
	done := make(map[string]bool)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return done, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			done[line] = true
		}
	}
	return done, nil
}

func appendState(path, recordURL string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(recordURL + "\n")
	return err
}
