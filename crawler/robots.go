package crawler

import (
	"bufio"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const RobotsUserAgent = "PageLeftBot/1.0 (+https://pageleft.cc)"

// RobotsChecker fetches and caches robots.txt per host,
// then checks whether a URL path is allowed.
type RobotsChecker struct {
	client *http.Client

	mu    sync.Mutex
	cache map[string]*robotsEntry
}

type robotsEntry struct {
	rules     []disallowRule
	fetchedAt time.Time
}

type disallowRule struct {
	agent    string // "*" or specific
	disallow string // path prefix
	allow    string // path prefix (overrides disallow)
}

func NewRobotsChecker(client *http.Client) *RobotsChecker {
	return &RobotsChecker{
		client: client,
		cache:  make(map[string]*robotsEntry),
	}
}

// IsAllowed checks if the given URL is allowed by robots.txt.
// Returns true if allowed or if robots.txt can't be fetched.
func (rc *RobotsChecker) IsAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}

	entry := rc.getRobots(u.Scheme + "://" + u.Host)
	if entry == nil {
		return true // no robots.txt or fetch failed — allow
	}

	return entry.isAllowed(u.Path)
}

func (rc *RobotsChecker) getRobots(origin string) *robotsEntry {
	rc.mu.Lock()
	entry, ok := rc.cache[origin]
	rc.mu.Unlock()

	if ok && time.Since(entry.fetchedAt) < 1*time.Hour {
		return entry
	}

	// Fetch robots.txt
	resp, err := rc.client.Get(origin + "/robots.txt")
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		// Cache the miss so we don't retry constantly
		entry = &robotsEntry{fetchedAt: time.Now()}
		rc.mu.Lock()
		rc.cache[origin] = entry
		rc.mu.Unlock()
		return nil
	}
	defer resp.Body.Close()

	rules := parseRobotsTxt(resp)
	entry = &robotsEntry{rules: rules, fetchedAt: time.Now()}

	rc.mu.Lock()
	rc.cache[origin] = entry
	rc.mu.Unlock()

	return entry
}

func parseRobotsTxt(resp *http.Response) []disallowRule {
	var rules []disallowRule
	scanner := bufio.NewScanner(resp.Body)
	currentAgent := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(parts[0]))
		val := strings.TrimSpace(parts[1])
		// Strip inline comments
		if idx := strings.Index(val, " #"); idx >= 0 {
			val = strings.TrimSpace(val[:idx])
		}

		switch key {
		case "user-agent":
			currentAgent = strings.ToLower(val)
		case "disallow":
			if val != "" && currentAgent != "" {
				rules = append(rules, disallowRule{agent: currentAgent, disallow: val})
			}
		case "allow":
			if val != "" && currentAgent != "" {
				rules = append(rules, disallowRule{agent: currentAgent, allow: val})
			}
		}
	}
	return rules
}

func (e *robotsEntry) isAllowed(path string) bool {
	// Check rules for our bot name first, then wildcard
	for _, agent := range []string{"pageleftbot", "*"} {
		hasRules := false
		for _, r := range e.rules {
			if r.agent != agent {
				continue
			}
			hasRules = true
			// Allow rules take precedence over disallow when more specific
			if r.allow != "" && strings.HasPrefix(path, r.allow) {
				return true
			}
			if r.disallow != "" && strings.HasPrefix(path, r.disallow) {
				return false
			}
		}
		if hasRules {
			return true // has rules for this agent but none matched — allowed
		}
	}
	return true // no matching rules — allowed
}
