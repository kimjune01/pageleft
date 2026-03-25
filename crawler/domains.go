package crawler

import (
	"bufio"
	"embed"
	"strings"
)

//go:embed copyleft_domains.txt
//go:embed blocked_domains.txt
//go:embed frontier_blocked_domains.txt
var domainFiles embed.FS

type DomainLicense struct {
	LicenseURL  string
	LicenseType string
}

var copyleftDomains map[string]DomainLicense
var blockedDomains map[string]bool
var frontierBlockedDomains map[string]bool

// NonPermissiveFilter is a persistent Bloom filter of domains known to be
// neither copyleft nor public domain. Seeded from blocked domain lists;
// grows as new domains fail license verification. Persisted to disk.
var NonPermissiveFilter *BloomFilter
var bloomFilterPath string

func init() {
	copyleftDomains = loadCopyleftDomains()
	blockedDomains = loadBlockedDomains()
	frontierBlockedDomains = loadDomainList("frontier_blocked_domains.txt")
}

// InitBloomFilter loads or creates the non-copyleft domain Bloom filter.
// Must be called after init with the DB path to determine storage location.
func InitBloomFilter(dbDir string) {
	bloomFilterPath = dbDir + "/nonpermissive.bloom"
	NonPermissiveFilter = LoadBloomFilter(bloomFilterPath, frontierBlockedDomains, blockedDomains)
}

func loadCopyleftDomains() map[string]DomainLicense {
	m := make(map[string]DomainLicense)
	data, err := domainFiles.ReadFile("copyleft_domains.txt")
	if err != nil {
		return m
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		m[parts[0]] = DomainLicense{
			LicenseType: parts[1],
			LicenseURL:  parts[2],
		}
	}
	return m
}

func loadBlockedDomains() map[string]bool {
	return loadDomainList("blocked_domains.txt")
}

func loadDomainList(filename string) map[string]bool {
	m := make(map[string]bool)
	data, err := domainFiles.ReadFile(filename)
	if err != nil {
		return m
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m[line] = true
	}
	return m
}

// IsFrontierBlocked returns true if the URL's domain should not enter the frontier.
// Fast path: Bloom filter lookup (O(1), no false negatives).
// Slow path: exact set with subdomain matching (for domains not in the Bloom filter).
func IsFrontierBlocked(rawURL string) bool {
	domain := ExtractDomain(rawURL)
	if domain == "" {
		return true
	}
	// Bloom filter: fast rejection for domains that are neither copyleft nor public domain
	if NonPermissiveFilter.Contains(domain) {
		return true
	}
	// Exact set: catches subdomains (e.g., m.facebook.com matches facebook.com)
	return matchDomain(frontierBlockedDomains, domain)
}

// LearnNonPermissive adds a domain to the Bloom filter after a page from that
// domain fails license verification (no copyleft or public domain license found).
// Persists to disk so the learning survives restarts.
func LearnNonPermissive(rawURL string) {
	domain := ExtractDomain(rawURL)
	if domain == "" || NonPermissiveFilter == nil {
		return
	}
	NonPermissiveFilter.Add(domain)
	if bloomFilterPath != "" {
		NonPermissiveFilter.Save(bloomFilterPath)
	}
}

// matchDomain checks if domain matches any entry exactly or as a subdomain.
func matchDomain(domains map[string]bool, domain string) bool {
	if domains[domain] {
		return true
	}
	// Check if domain is a subdomain of any blocked domain
	for d := range domains {
		if strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

// matchCopyleftDomain checks if domain matches any copyleft entry exactly or as a subdomain.
func matchCopyleftDomain(domains map[string]DomainLicense, domain string) (DomainLicense, bool) {
	if dl, ok := domains[domain]; ok {
		return dl, true
	}
	for d, dl := range domains {
		if strings.HasSuffix(domain, "."+d) {
			return dl, true
		}
	}
	return DomainLicense{}, false
}

// CheckDomain returns a LicenseInfo if the domain is in the copyleft allowlist,
// nil if unknown, or an error string if blocked.
func CheckDomain(rawURL string) (*LicenseInfo, bool, string) {
	domain := ExtractDomain(rawURL)
	if domain == "" {
		return nil, false, ""
	}

	if matchDomain(blockedDomains, domain) {
		return nil, true, "domain blocked: platform ToS overrides author license"
	}

	if dl, ok := matchCopyleftDomain(copyleftDomains, domain); ok {
		return &LicenseInfo{URL: dl.LicenseURL, Type: dl.LicenseType}, false, ""
	}

	return nil, false, ""
}
