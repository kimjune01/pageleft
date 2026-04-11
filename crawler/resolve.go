package crawler

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/kimjune01/pageleft/platform"
)

// Action is the outcome of URL resolution.
type Action int

const (
	Allow Action = iota // Proceed with fetch and license detection
	Block               // Do not fetch, do not add to frontier
	Skip                // Not indexable but not worth learning (e.g. non-HTTP)
)

// Resolution is the Filter stage output for a URL.
// One function, one decision.
type Resolution struct {
	Action   Action
	License  *LicenseInfo // non-nil if license already determined (domain or forge API)
	FetchURL string       // rewritten URL if needed (Wikipedia REST API, raw README)
	Reason   string       // why blocked/skipped
}

// Resolve runs the full filter chain for a URL.
// Chain order: protocol → blocked domain → Bloom filter → forge → Wikipedia → copyleft domain → allow.
func Resolve(rawURL string) Resolution {
	// 1. Protocol check
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return Resolution{Action: Skip, Reason: "not HTTP"}
	}

	// 1b. Binary file extension — not indexable text
	if isBinaryURL(rawURL) {
		return Resolution{Action: Block, Reason: "binary file extension"}
	}

	domain := ExtractDomain(rawURL)
	if domain == "" {
		return Resolution{Action: Skip, Reason: "no domain"}
	}

	// 1c. Non-English Wikipedia/Wikimedia — out of scope for an English index.
	// These pass license verification legitimately (CC BY-SA in their HTML)
	// but contribute noise without serving the audience.
	if isNonEnglishWikimedia(domain) {
		return Resolution{Action: Block, Reason: "non-English Wikimedia out of scope"}
	}

	// 1d. MediaWiki meta-namespace pages — Category:, Special:, Help:, User:,
	// Wikipedia:, Wiktionary:, Talk:, File:, Template:, Portal:, etc.
	// These are navigation/admin pages, not content. They appear on every
	// MediaWiki site and dilute the index. Must come before the copyleft
	// allowlist so en.wikipedia.org/wiki/Category: pages get rejected.
	if isMediaWikiMetaPage(rawURL) {
		return Resolution{Action: Block, Reason: "MediaWiki meta-namespace page"}
	}

	// 1e. MediaWiki edit/admin URLs — ?action=edit, history, raw, etc.
	// These serve admin forms ("You do not have permission to edit"),
	// not article content.
	if isWikiActionURL(rawURL) {
		return Resolution{Action: Block, Reason: "MediaWiki action URL"}
	}

	// 1f. Site-specific blocked path prefixes (catalog/listing pages).
	if isBlockedPathPrefix(rawURL) {
		return Resolution{Action: Block, Reason: "blocked path prefix"}
	}

	// 2. Blocked domain (exact set — platform ToS)
	if matchDomain(blockedDomains, domain) {
		return Resolution{Action: Block, Reason: "domain blocked: platform ToS"}
	}

	// 3. Copyleft domain allowlist (exact set — skip per-page detection)
	// Must precede Bloom filter: a domain added to the allowlist after being
	// learned as non-permissive would otherwise stay blocked forever.
	if dl, ok := matchCopyleftDomain(copyleftDomains, domain); ok {
		return Resolution{
			Action:  Allow,
			License: &LicenseInfo{URL: dl.LicenseURL, Type: dl.LicenseType},
		}
	}

	// 4. Code forge (GitHub, Codeberg) — check license via API, fetch README.
	// Must precede Bloom filter: github.com hosts both permissive and copyleft
	// repos, so domain-level blocking would reject all of them.
	if owner, repo, ok := parseForgeURL(rawURL); ok {
		return resolveForge(rawURL, owner, repo)
	}

	// 5. Bloom filters (static + dynamic — non-permissive domains)
	if IsNonPermissive(domain) {
		return Resolution{Action: Block, Reason: "domain non-permissive (bloom)"}
	}

	// 6. Frontier blocked (superset: paywalls, social, noise)
	// Must precede Wikimedia allowlist: wikiquote.org is a valid Wikimedia
	// project with CC BY-SA, but we block it as low-signal content.
	if matchDomain(frontierBlockedDomains, domain) {
		return Resolution{Action: Block, Reason: "domain not indexable"}
	}

	// 7. Wikipedia/Wikimedia — rewrite to REST API, license is CC BY-SA
	if title, ok := parseWikimediaURL(rawURL); ok {
		u, _ := url.Parse(rawURL)
		return Resolution{
			Action:   Allow,
			License:  &LicenseInfo{URL: "https://creativecommons.org/licenses/by-sa/3.0/", Type: "CC BY-SA"},
			FetchURL: fmt.Sprintf("https://%s/api/rest_v1/page/html/%s", u.Host, title),
		}
	}

	// 8. Unknown — allow, per-page detection at fetch time
	return Resolution{Action: Allow}
}

// ShouldBlockFrontier returns true if the URL should not enter the frontier.
// Matches the platform.URLFilter signature for use with InsertPageWithLinks and PruneFrontier.
func ShouldBlockFrontier(rawURL string) bool {
	return !ResolveForFrontier(rawURL)
}

// ShouldBlockFrontierFrom returns a URLFilter that also blocks same-site
// links for massive corpora (currently Wikipedia). Cross-domain links into
// Wikipedia still pass; only Wikipedia→Wikipedia links are blocked.
func ShouldBlockFrontierFrom(sourceURL string) func(string) bool {
	sourceIsWiki := isWikipediaDomain(ExtractDomain(sourceURL))
	return func(targetURL string) bool {
		if ShouldBlockFrontier(targetURL) {
			return true
		}
		// Block any Wikipedia → Wikipedia link, regardless of language.
		// English articles dump 50-200 language-sidebar links per page, and
		// non-English Wikipedia articles spider themselves the same way.
		if sourceIsWiki && isWikipediaDomain(ExtractDomain(targetURL)) {
			return true
		}
		return false
	}
}

// isWikipediaDomain returns true for any *.wikipedia.org subdomain.
func isWikipediaDomain(domain string) bool {
	return domain == "wikipedia.org" || strings.HasSuffix(domain, ".wikipedia.org")
}

// mediaWikiMetaNamespaces are URL path prefixes for non-content pages on
// MediaWiki sites (Category:, Special:, etc.). Loaded from
// crawler/mediawiki_meta_namespaces.txt at init time.
var mediaWikiMetaNamespaces []string

// isMediaWikiMetaPage returns true if the URL points to a MediaWiki
// meta-namespace page (Category:, Special:, etc.) on any wiki.
func isMediaWikiMetaPage(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for _, prefix := range mediaWikiMetaNamespaces {
		if strings.HasPrefix(u.Path, prefix) {
			return true
		}
	}
	return false
}

// wikiActionValues are MediaWiki action= query values that serve admin
// or edit forms instead of article content.
var wikiActionValues = map[string]bool{
	"edit":    true,
	"history": true,
	"raw":     true,
	"submit":  true,
	"info":    true,
	"delete":  true,
	"protect": true,
	"move":    true,
	"watch":   true,
	"purge":   true,
}

// isWikiActionURL returns true if the URL has an ?action= query parameter
// targeting a MediaWiki admin form rather than article content.
// Catches /w/index.php?title=Foo&action=edit and similar.
func isWikiActionURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	action := u.Query().Get("action")
	return action != "" && wikiActionValues[action]
}

// blockedPathPrefixes are site-specific URL path prefixes for catalog/listing
// pages that aren't content. Loaded from crawler/blocked_path_prefixes.txt.
var blockedPathPrefixes []string

// isBlockedPathPrefix returns true if the URL path starts with any of the
// configured catalog/navigation prefixes (e.g. /ebooks/bookshelf/).
func isBlockedPathPrefix(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for _, prefix := range blockedPathPrefixes {
		if strings.HasPrefix(u.Path, prefix) {
			return true
		}
	}
	return false
}

// wikimediaProjects are leading-dot project domains (e.g. ".wikipedia.org").
// Loaded from crawler/wikimedia_projects.txt at init time.
var wikimediaProjects []string

// isNonEnglishWikimedia returns true for Wikipedia/Wikibooks/Wikisource/etc.
// in any language other than English. Wikimedia hosts follow <lang>.<project>.org;
// only the en. subdomain is allowed.
func isNonEnglishWikimedia(domain string) bool {
	for _, project := range wikimediaProjects {
		if strings.HasSuffix(domain, project) {
			if domain == "en"+project {
				return false
			}
			return true
		}
	}
	return false
}

// ResolveForFrontier is a lighter check for AddToFrontier.
// Skips forge API calls (expensive). Just checks domain-level gates.
func ResolveForFrontier(rawURL string) bool {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return false
	}
	if isBinaryURL(rawURL) {
		return false
	}
	domain := ExtractDomain(rawURL)
	if domain == "" {
		return false
	}
	if matchDomain(blockedDomains, domain) {
		return false
	}
	if isNonEnglishWikimedia(domain) {
		return false
	}
	if IsNonPermissive(domain) {
		return false
	}
	if matchDomain(frontierBlockedDomains, domain) {
		return false
	}
	return true
}

// isBinaryURL returns true if the URL path ends with a non-text file extension.
func isBinaryURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := strings.ToLower(u.Path)
	for _, ext := range binaryExtensions {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// binaryExtensions are non-text file extensions used by isBinaryURL.
// Loaded from crawler/binary_extensions.txt at init time.
var binaryExtensions []string

// CanonicalPageURL normalizes a URL for storage in the pages table:
// strips tracking params and collapses forge deep paths to owner/repo.
// Does NOT upgrade scheme (http stays http) so HTTP-only sites and tests
// keep working. Frontier dedup uses platform.NormalizeURL which is stricter.
func CanonicalPageURL(rawURL string) string {
	stripped := platform.StripTrackingParams(rawURL)
	if owner, repo, ok := parseForgeURL(stripped); ok {
		host := ExtractDomain(stripped)
		return "https://" + host + "/" + owner + "/" + repo
	}
	return stripped
}

// parseForgeURL extracts owner/repo from any github.com or codeberg.org URL.
// Matches github.com/{owner}/{repo}, github.com/{owner}/{repo}/tree/main, etc.
// Only the first two path segments (owner/repo) matter — the README is always fetched.
func parseForgeURL(rawURL string) (string, string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "codeberg.org" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner, repo := parts[0], parts[1]
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}

// parseWikimediaURL extracts the article title from Wikipedia/Wikibooks/Wikisource URLs.
func parseWikimediaURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	isWikimedia := host == "en.wikipedia.org" ||
		host == "en.wikibooks.org" ||
		host == "en.wikisource.org"
	if !isWikimedia {
		return "", false
	}
	if !strings.HasPrefix(u.Path, "/wiki/") {
		return "", false
	}
	title := strings.TrimPrefix(u.Path, "/wiki/")
	if title == "" {
		return "", false
	}
	return title, true
}
