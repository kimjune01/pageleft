package crawler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Copyleft SPDX identifiers.
var copyleftSPDX = map[string]bool{
	"GPL-2.0": true, "GPL-2.0-only": true, "GPL-2.0-or-later": true,
	"GPL-3.0": true, "GPL-3.0-only": true, "GPL-3.0-or-later": true,
	"AGPL-3.0": true, "AGPL-3.0-only": true, "AGPL-3.0-or-later": true,
	"LGPL-2.1": true, "LGPL-2.1-only": true, "LGPL-2.1-or-later": true,
	"LGPL-3.0": true, "LGPL-3.0-only": true, "LGPL-3.0-or-later": true,
	"MPL-2.0": true,
	"CC-BY-SA-3.0": true, "CC-BY-SA-4.0": true,
	"GFDL-1.3": true, "GFDL-1.3-only": true, "GFDL-1.3-or-later": true,
	"CC0-1.0": true, "Unlicense": true,
}

var forgeClient = &http.Client{Timeout: 15 * time.Second}

func resolveForge(pageURL, owner, repo string) Resolution {
	host := ExtractDomain(pageURL)

	var spdx, licenseURL string
	if host == "github.com" {
		spdx, licenseURL = githubLicense(owner, repo)
	} else {
		spdx, licenseURL = codebergLicense(owner, repo)
	}

	if spdx == "" || !copyleftSPDX[spdx] {
		return Resolution{Action: Block, Reason: fmt.Sprintf("no copyleft license (spdx: %s)", spdx)}
	}

	// Rewrite to raw README
	var fetchURL string
	if host == "github.com" {
		fetchURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/README.md", owner, repo)
	} else {
		fetchURL = fmt.Sprintf("https://codeberg.org/%s/%s/raw/branch/main/README.md", owner, repo)
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

	return detectLicenseFromFile(owner, repo)
}

// detectLicenseFromFile fetches the raw LICENSE/COPYING file and detects copyleft by keyword.
func detectLicenseFromFile(owner, repo string) (string, string) {
	for _, filename := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"} {
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/%s", owner, repo, filename)
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
		text := strings.ToLower(string(body))

		licenseURL := fmt.Sprintf("https://github.com/%s/%s/blob/HEAD/%s", owner, repo, filename)
		if strings.Contains(text, "gnu affero general public license") {
			return "AGPL-3.0", licenseURL
		}
		if strings.Contains(text, "gnu general public license") {
			if strings.Contains(text, "version 3") {
				return "GPL-3.0", licenseURL
			}
			return "GPL-2.0", licenseURL
		}
		if strings.Contains(text, "gnu lesser general public license") {
			return "LGPL-2.1", licenseURL
		}
		if strings.Contains(text, "mozilla public license") {
			return "MPL-2.0", licenseURL
		}
		if strings.Contains(text, "creative commons") && strings.Contains(text, "sharealike") {
			return "CC-BY-SA-4.0", licenseURL
		}
		if strings.Contains(text, "creative commons") && strings.Contains(text, "cc0") {
			return "CC0-1.0", licenseURL
		}
		if strings.Contains(text, "unlicense") || strings.Contains(text, "public domain") {
			return "Unlicense", licenseURL
		}
		return "", ""
	}
	return "", ""
}

func codebergLicense(owner, repo string) (string, string) {
	apiURL := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s", owner, repo)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", RobotsUserAgent)
	resp, err := forgeClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return "", ""
	}
	defer resp.Body.Close()
	var result struct {
		License string `json:"license"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.License, fmt.Sprintf("https://codeberg.org/%s/%s", owner, repo)
}
