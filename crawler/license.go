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

// copyleft license URL patterns
var copyleftPatterns = []struct {
	substring string
	name      string
}{
	// Creative Commons ShareAlike
	{"creativecommons.org/licenses/by-sa/", "CC BY-SA"},
	// GPL family
	{"gnu.org/licenses/agpl", "AGPL"},
	{"gnu.org/licenses/lgpl", "LGPL"},
	{"gnu.org/licenses/gpl", "GPL"},
	{"opensource.org/licenses/AGPL", "AGPL"},
	{"opensource.org/licenses/LGPL", "LGPL"},
	{"opensource.org/licenses/GPL", "GPL"},
	// GFDL
	{"gnu.org/licenses/fdl", "GFDL"},
	{"gnu.org/copyleft/fdl", "GFDL"},
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

// <meta name="license" content="...">
func findMetaLicense(n *html.Node) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "meta" {
		name := attr(n, "name")
		if strings.EqualFold(name, "license") {
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

// Footer heuristic: look for links containing copyleft URLs in footer-like elements
func findFooterLicense(n *html.Node) *LicenseInfo {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := attr(n, "href")
		if href != "" {
			if li := checkURL(href); li != nil {
				return li
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if li := findFooterLicense(c); li != nil {
			return li
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
