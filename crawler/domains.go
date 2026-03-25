package crawler

import (
	"bufio"
	"embed"
	"fmt"
	"os"
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

// Two Bloom filters, one concern each:
//   StaticFilter  — rebuilt from text files + UT1 on deploy. Deterministic.
//   DynamicFilter — starts empty, grows from runtime license failures. Never rebuilt.
// Either one blocking = blocked.
var StaticFilter *BloomFilter
var DynamicFilter *BloomFilter
var dynamicFilterPath string

func init() {
	copyleftDomains = loadCopyleftDomains()
	blockedDomains = loadBlockedDomains()
	frontierBlockedDomains = loadDomainList("frontier_blocked_domains.txt")
}

// InitBloomFilters creates both filters.
// Static: always rebuilt from text file seeds (no persistence — deterministic).
// Dynamic: loaded from disk if exists, otherwise starts empty.
func InitBloomFilters(dbDir string) {
	// Static: rebuild every startup from text files
	StaticFilter = NewBloomFilter(5_000_000, 0.001)
	for d := range frontierBlockedDomains {
		StaticFilter.Add(d)
	}
	for d := range blockedDomains {
		StaticFilter.Add(d)
	}

	// Dynamic: persist across restarts, only grows
	dynamicFilterPath = dbDir + "/learned-nonpermissive.bloom"
	DynamicFilter = LoadBloomFilterN(dynamicFilterPath, 100_000, 0.001)
}

// SeedStatic adds domains from a file to the static filter.
// Used to import public blocklists (e.g., UT1) at deploy time.
// Does NOT persist — the static filter is rebuilt every startup.
func SeedStatic(path string) (int, error) {
	if StaticFilter == nil {
		return 0, fmt.Errorf("static filter not initialized")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		domain := strings.TrimSpace(scanner.Text())
		if domain != "" && !strings.HasPrefix(domain, "#") {
			StaticFilter.Add(domain)
			n++
		}
	}
	return n, nil
}

// IsNonPermissive checks both filters. Either blocking = blocked.
func IsNonPermissive(domain string) bool {
	if StaticFilter != nil && StaticFilter.Contains(domain) {
		return true
	}
	if DynamicFilter != nil && DynamicFilter.Contains(domain) {
		return true
	}
	return false
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
// Bloom filters check exact domain; matchDomain catches subdomains
// (e.g., m.facebook.com matching facebook.com in the text file).
func IsFrontierBlocked(rawURL string) bool {
	domain := ExtractDomain(rawURL)
	if domain == "" {
		return true
	}
	if IsNonPermissive(domain) {
		return true
	}
	return matchDomain(frontierBlockedDomains, domain)
}

// LearnNonPermissive adds a domain to the dynamic Bloom filter after a page
// fails license verification. Only the dynamic filter grows at runtime;
// the static filter is rebuilt from text files on deploy.
func LearnNonPermissive(rawURL string) {
	domain := ExtractDomain(rawURL)
	if domain == "" || DynamicFilter == nil {
		return
	}
	DynamicFilter.Add(domain)
	if dynamicFilterPath != "" {
		DynamicFilter.Save(dynamicFilterPath)
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
