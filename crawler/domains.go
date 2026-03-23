package crawler

import (
	"bufio"
	"embed"
	"strings"
)

//go:embed copyleft_domains.txt
//go:embed blocked_domains.txt
var domainFiles embed.FS

type DomainLicense struct {
	LicenseURL  string
	LicenseType string
}

var copyleftDomains map[string]DomainLicense
var blockedDomains map[string]bool

func init() {
	copyleftDomains = loadCopyleftDomains()
	blockedDomains = loadBlockedDomains()
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
	m := make(map[string]bool)
	data, err := domainFiles.ReadFile("blocked_domains.txt")
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
