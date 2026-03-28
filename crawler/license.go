package crawler

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

type LicenseInfo struct {
	URL  string
	Type string // e.g. "CC BY-SA 4.0", "GPL-3.0", "AGPL-3.0"
}

// Composable copyleft license URL patterns.
// Excludes GPL-2.0, LGPL-2.1, and GFDL — not composable with GPL-3.0+/AGPL-3.0.
// Order matters: more specific patterns must come before less specific ones.
var copyleftPatterns = []struct {
	substring string
	name      string
}{
	// Creative Commons ShareAlike
	{"creativecommons.org/licenses/by-sa/", "CC BY-SA"},
	// AGPL (always v3)
	{"gnu.org/licenses/agpl", "AGPL"},
	{"opensource.org/licenses/AGPL", "AGPL"},
	// LGPL — only v3+
	{"gnu.org/licenses/lgpl-3", "LGPL"},
	{"opensource.org/licenses/LGPL-3", "LGPL"},
	// GPL — only v3+
	{"gnu.org/licenses/gpl-3", "GPL-3.0"},
	{"opensource.org/licenses/GPL-3", "GPL-3.0"},
	// MPL
	{"mozilla.org/MPL", "MPL"},
	{"opensource.org/licenses/MPL", "MPL"},
}

// public domain URL patterns — nobody can exclude anyone, ever
var publicDomainPatterns = []struct {
	substring string
	name      string
}{
	// CC0
	{"creativecommons.org/publicdomain/zero/", "CC0"},
	// Public Domain Mark
	{"creativecommons.org/publicdomain/mark/", "Public Domain"},
	// Unlicense
	{"unlicense.org", "Unlicense"},
}

// DetectLicense extracts license info from an HTML document.
// Priority: <link rel="license"> -> JSON-LD -> <a rel="license"> -> <meta name="license"> -> footer heuristic
func DetectLicense(doc *html.Node) *LicenseInfo {
	if li := findLinkRelLicense(doc); li != nil {
		return li
	}
	if li := findJSONLDLicense(doc); li != nil {
		return li
	}
	if li := findAnchorRelLicense(doc); li != nil {
		return li
	}
	if li := findMetaLicense(doc); li != nil {
		return li
	}
	if li := findFooterLicense(doc); li != nil {
		return li
	}
	return nil
}

func isCopyleft(url string) (string, bool) {
	lower := strings.ToLower(url)
	for _, p := range copyleftPatterns {
		if strings.Contains(lower, strings.ToLower(p.substring)) {
			return p.name, true
		}
	}
	return "", false
}

func isPublicDomain(url string) (string, bool) {
	lower := strings.ToLower(url)
	for _, p := range publicDomainPatterns {
		if strings.Contains(lower, strings.ToLower(p.substring)) {
			return p.name, true
		}
	}
	return "", false
}

// isNonExclusive returns true for copyleft or public domain licenses.
// Both guarantee nobody can exclude anyone from using the content.
func isNonExclusive(url string) (string, bool) {
	if name, ok := isCopyleft(url); ok {
		return name, true
	}
	return isPublicDomain(url)
}

func checkURL(url string) *LicenseInfo {
	name, ok := isNonExclusive(url)
	if !ok {
		return nil
	}
	return &LicenseInfo{URL: url, Type: name}
}

// <link rel="license" href="...">
func findLinkRelLicense(n *html.Node) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "link" {
		rel := attr(n, "rel")
		if strings.EqualFold(rel, "license") {
			href := attr(n, "href")
			if href != "" {
				return checkURL(href)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if li := findLinkRelLicense(c); li != nil {
			return li
		}
	}
	return nil
}

// JSON-LD: <script type="application/ld+json"> with "license" field
func findJSONLDLicense(n *html.Node) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "script" {
		if strings.EqualFold(attr(n, "type"), "application/ld+json") {
			if n.FirstChild != nil {
				text := n.FirstChild.Data
				// Simple extraction — look for "license": "url"
				if idx := strings.Index(text, `"license"`); idx >= 0 {
					rest := text[idx+len(`"license"`):]
					// find the value after ":"
					if colonIdx := strings.Index(rest, `"`); colonIdx >= 0 {
						rest = rest[colonIdx+1:]
						if endIdx := strings.Index(rest, `"`); endIdx >= 0 {
							url := rest[:endIdx]
							return checkURL(url)
						}
					}
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if li := findJSONLDLicense(c); li != nil {
			return li
		}
	}
	return nil
}

// <a rel="license" href="...">
func findAnchorRelLicense(n *html.Node) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "a" {
		rel := attr(n, "rel")
		if strings.EqualFold(rel, "license") {
			href := attr(n, "href")
			if href != "" {
				return checkURL(href)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if li := findAnchorRelLicense(c); li != nil {
			return li
		}
	}
	return nil
}

// <meta name="license" content="..."> or <meta name="dc.rights" content="...">
func findMetaLicense(n *html.Node) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "meta" {
		name := attr(n, "name")
		if strings.EqualFold(name, "license") || strings.EqualFold(name, "dc.rights") {
			content := attr(n, "content")
			if content != "" {
				return checkURL(content)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if li := findMetaLicense(c); li != nil {
			return li
		}
	}
	return nil
}

// Footer heuristic: look for links containing copyleft URLs in footer-like elements.
// Also matches anchor text like "CC BY-SA 4.0" inside <footer> elements, even if
// the href points to a local license page instead of creativecommons.org.
func findFooterLicense(n *html.Node) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "footer" {
		return findFooterLicenseInner(n, true)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if li := findFooterLicense(c); li != nil {
			return li
		}
	}
	return nil
}

func findFooterLicenseInner(n *html.Node, inFooter bool) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := attr(n, "href")
		// First: check if href itself is a copyleft URL
		if href != "" {
			if li := checkURL(href); li != nil {
				return li
			}
		}
		// Inside <footer>: also check anchor text for license names
		if inFooter {
			text := collectText(n)
			if li := matchFooterLicenseText(text); li != nil {
				return li
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if li := findFooterLicenseInner(c, inFooter); li != nil {
			return li
		}
	}
	return nil
}

// collectText returns all text content under a node.
func collectText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(collectText(c))
	}
	return sb.String()
}

// footerLicensePatterns maps anchor text substrings to license info.
var footerLicensePatterns = []struct {
	substring string
	info      LicenseInfo
}{
	{"CC BY-SA", LicenseInfo{URL: "https://creativecommons.org/licenses/by-sa/4.0/", Type: "CC BY-SA"}},
	{"CC0", LicenseInfo{URL: "https://creativecommons.org/publicdomain/zero/1.0/", Type: "CC0"}},
	{"AGPL", LicenseInfo{URL: "https://www.gnu.org/licenses/agpl-3.0.html", Type: "AGPL"}},
	{"GPL-3", LicenseInfo{URL: "https://www.gnu.org/licenses/gpl-3.0.html", Type: "GPL-3.0"}},
	{"GPLv3", LicenseInfo{URL: "https://www.gnu.org/licenses/gpl-3.0.html", Type: "GPL-3.0"}},
}

// matchFooterLicenseText checks if anchor text contains a known copyleft license name.
func matchFooterLicenseText(text string) *LicenseInfo {
	upper := strings.ToUpper(strings.TrimSpace(text))
	for _, p := range footerLicensePatterns {
		if strings.Contains(upper, strings.ToUpper(p.substring)) {
			li := p.info
			return &li
		}
	}
	return nil
}

// ExtractDomain returns the hostname from a URL, stripping "www." prefix.
func ExtractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	return host
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
