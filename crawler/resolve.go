package crawler

import (
	"fmt"
	"net/url"
	"strings"
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

	// 4. Bloom filters (static + dynamic — non-permissive domains)
	if IsNonPermissive(domain) {
		return Resolution{Action: Block, Reason: "domain non-permissive (bloom)"}
	}

	// 5. Code forge (GitHub, Codeberg) — check license via API, fetch README
	if owner, repo, ok := parseForgeURL(rawURL); ok {
		return resolveForge(rawURL, owner, repo)
	}

	// 6. Wikipedia/Wikimedia — rewrite to REST API, license is CC BY-SA
	if title, ok := parseWikimediaURL(rawURL); ok {
		u, _ := url.Parse(rawURL)
		return Resolution{
			Action:   Allow,
			License:  &LicenseInfo{URL: "https://creativecommons.org/licenses/by-sa/3.0/", Type: "CC BY-SA"},
			FetchURL: fmt.Sprintf("https://%s/api/rest_v1/page/html/%s", u.Host, title),
		}
	}

	// 7. Frontier blocked (superset: paywalls, social, noise)
	if matchDomain(frontierBlockedDomains, domain) {
		return Resolution{Action: Block, Reason: "domain not indexable"}
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

// isNonEnglishWikimedia returns true for Wikipedia/Wikibooks/Wikisource/etc.
// in any language other than English. The English subdomains are explicitly
// allowed via the copyleft allowlist; everything else is out of scope.
func isNonEnglishWikimedia(domain string) bool {
	// Wikimedia hosts are <lang>.<project>.org
	wikimediaProjects := []string{
		".wikipedia.org", ".wikibooks.org", ".wikisource.org",
		".wiktionary.org", ".wikiquote.org", ".wikinews.org",
		".wikiversity.org", ".wikivoyage.org",
	}
	for _, project := range wikimediaProjects {
		if strings.HasSuffix(domain, project) {
			// Allow only en.<project>.org
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

var binaryExtensions = []string{
	// Images
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico", ".tiff", ".tif", ".avif",
	// Audio/video
	".mp3", ".mp4", ".wav", ".ogg", ".webm", ".flac", ".avi", ".mov", ".mkv",
	// Archives
	".zip", ".tar", ".gz", ".bz2", ".xz", ".rar", ".7z",
	// Binaries
	".exe", ".dll", ".so", ".dylib", ".bin", ".dmg", ".iso", ".deb", ".rpm",
	// Documents (non-text, but not PDF — PDF text extraction is supported)
	".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
	// Fonts
	".woff", ".woff2", ".ttf", ".otf", ".eot",
}

// CanonicalPageURL normalizes a URL for storage. Collapses forge deep paths
// to owner/repo so the same repo isn't stored multiple times.
func CanonicalPageURL(rawURL string) string {
	if owner, repo, ok := parseForgeURL(rawURL); ok {
		host := ExtractDomain(rawURL)
		return "https://" + host + "/" + owner + "/" + repo
	}
	return rawURL
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
