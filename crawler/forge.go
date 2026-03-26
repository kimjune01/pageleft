package crawler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GitHubToken is read from GITHUB_PUBLIC_API_KEY at init.
// Unauthenticated: 60 req/hr. Authenticated: 5,000 req/hr.
var GitHubToken string

func init() {
	GitHubToken = os.Getenv("GITHUB_PUBLIC_API_KEY")
}

// Composable SPDX identifiers — licenses that compose with GPL-3.0+/AGPL-3.0.
// Excludes GPL-2.0-only, LGPL-2.1-only, and GFDL (invariant sections break remixing).
var copyleftSPDX = map[string]bool{
	"GPL-3.0": true, "GPL-3.0-only": true, "GPL-3.0-or-later": true,
	"AGPL-3.0": true, "AGPL-3.0-only": true, "AGPL-3.0-or-later": true,
	"LGPL-3.0": true, "LGPL-3.0-only": true, "LGPL-3.0-or-later": true,
	"MPL-2.0": true,
	"CC-BY-SA-3.0": true, "CC-BY-SA-4.0": true,
	"CC0-1.0": true, "Unlicense": true,
}

var forgeClient = &http.Client{Timeout: 15 * time.Second}

func resolveForge(pageURL, owner, repo string) Resolution {
	host := ExtractDomain(pageURL)

	var spdx, licenseURL, branch string
	if host == "github.com" {
		spdx, licenseURL = githubLicense(owner, repo)
		branch = "HEAD"
	} else {
		spdx, licenseURL, branch = codebergLicenseFull(owner, repo)
	}

	if spdx == "" || !copyleftSPDX[spdx] {
		return Resolution{Action: Block, Reason: fmt.Sprintf("no copyleft license (spdx: %s)", spdx)}
	}

	// Rewrite to raw README
	var fetchURL string
	if host == "github.com" {
		fetchURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/README.md", owner, repo)
	} else {
		fetchURL = fmt.Sprintf("https://codeberg.org/%s/%s/raw/branch/%s/README.md", owner, repo, branch)
	}

	return Resolution{
		Action:   Allow,
		License:  &LicenseInfo{URL: licenseURL, Type: spdx},
		FetchURL: fetchURL,
	}
}

func githubLicense(owner, repo string) (string, string) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", RobotsUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	if GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+GitHubToken)
	}
	resp, err := forgeClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return "", ""
	}
	defer resp.Body.Close()
	var result struct {
		License struct {
			SPDX string `json:"spdx_id"`
		} `json:"license"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	licenseURL := fmt.Sprintf("https://github.com/%s/%s/blob/HEAD/LICENSE", owner, repo)
	if result.License.SPDX != "" && result.License.SPDX != "NOASSERTION" {
		return result.License.SPDX, licenseURL
	}

	return detectLicenseFromRaw(
		fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/", owner, repo),
		fmt.Sprintf("https://github.com/%s/%s/blob/HEAD/", owner, repo),
	)
}

func codebergLicenseFull(owner, repo string) (string, string, string) {
	apiURL := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s", owner, repo)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", RobotsUserAgent)
	resp, err := forgeClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return "", "", "main"
	}
	defer resp.Body.Close()
	var result struct {
		License       string `json:"license"`
		DefaultBranch string `json:"default_branch"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	branch := result.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	if result.License != "" && result.License != "NONE" && copyleftSPDX[result.License] {
		return result.License, fmt.Sprintf("https://codeberg.org/%s/%s", owner, repo), branch
	}

	// Fallback: fetch raw LICENSE file using the actual default branch
	spdx, licURL := detectLicenseFromRaw(
		fmt.Sprintf("https://codeberg.org/%s/%s/raw/branch/%s/", owner, repo, branch),
		fmt.Sprintf("https://codeberg.org/%s/%s/src/branch/%s/", owner, repo, branch),
	)
	return spdx, licURL, branch
}

// detectLicenseFromRaw tries to fetch LICENSE files from a raw base URL.
func detectLicenseFromRaw(rawBase, browseBase string) (string, string) {
	for _, filename := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"} {
		rawURL := rawBase + filename
		req, _ := http.NewRequest("GET", rawURL, nil)
		req.Header.Set("User-Agent", RobotsUserAgent)
		resp, err := forgeClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()

		spdx := matchLicenseText(string(body))
		if spdx != "" {
			return spdx, browseBase + filename
		}
		return "", "" // found a file but not copyleft
	}
	return "", ""
}

// matchLicenseText detects copyleft/public domain licenses by keyword.
func matchLicenseText(raw string) string {
	text := strings.ToLower(raw)
	if strings.Contains(text, "gnu affero general public license") {
		return "AGPL-3.0"
	}
	if strings.Contains(text, "gnu general public license") {
		if strings.Contains(text, "version 3") {
			return "GPL-3.0"
		}
		return "" // GPL-2.0 — not composable with GPL-3.0+
	}
	if strings.Contains(text, "gnu lesser general public license") {
		if strings.Contains(text, "version 3") {
			return "LGPL-3.0"
		}
		return "" // LGPL-2.1 — not composable with GPL-3.0+
	}
	if strings.Contains(text, "mozilla public license") {
		return "MPL-2.0"
	}
	if strings.Contains(text, "creative commons") && strings.Contains(text, "sharealike") {
		return "CC-BY-SA-4.0"
	}
	if strings.Contains(text, "creative commons") && strings.Contains(text, "cc0") {
		return "CC0-1.0"
	}
	if strings.Contains(text, "unlicense") || (strings.Contains(text, "public domain") && !strings.Contains(text, "not public domain")) {
		return "Unlicense"
	}
	return ""
}
