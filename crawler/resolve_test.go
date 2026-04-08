package crawler

import "testing"

func TestIsNonEnglishWikimedia(t *testing.T) {
	tests := []struct {
		domain string
		want   bool
	}{
		// English projects: allowed
		{"en.wikipedia.org", false},
		{"en.wikibooks.org", false},
		{"en.wikisource.org", false},
		{"en.wiktionary.org", false},

		// Non-English projects: blocked
		{"ps.wikipedia.org", true},
		{"de.wikipedia.org", true},
		{"fr.wikibooks.org", true},
		{"ja.wiktionary.org", true},
		{"zh.wikiquote.org", true},
		{"es.wikinews.org", true},

		// Non-Wikimedia domains: not blocked by this filter
		{"bartoszmilewski.com", false},
		{"github.com", false},
		{"june.kim", false},

		// Edge cases
		{"wikipedia.org", false}, // bare domain isn't a language subdomain
		{"", false},
	}

	for _, tt := range tests {
		got := isNonEnglishWikimedia(tt.domain)
		if got != tt.want {
			t.Errorf("isNonEnglishWikimedia(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestResolveBlocksNonEnglishWikimedia(t *testing.T) {
	res := Resolve("https://ps.wikipedia.org/wiki/Microsoft_Windows")
	if res.Action != Block {
		t.Errorf("Resolve(ps.wikipedia.org) = %v, want Block", res.Action)
	}
}

func TestIsMediaWikiMetaPage(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		// Meta-namespace pages: blocked
		{"https://en.wikipedia.org/wiki/Category:Animals", true},
		{"https://en.wikipedia.org/wiki/Special:Random", true},
		{"https://en.wikipedia.org/wiki/Help:Contents", true},
		{"https://en.wikipedia.org/wiki/User:Jimbo_Wales", true},
		{"https://en.wikipedia.org/wiki/Wikipedia:Multilingual_coordination", true},
		{"https://en.wiktionary.org/wiki/Category:Terms_with_Greek_translations", true},
		{"https://en.wiktionary.org/wiki/Wiktionary:Contact_us", true},
		{"https://en.wikibooks.org/wiki/Talk:Main_Page", true},
		{"https://en.wikipedia.org/wiki/File:Logo.png", true},
		{"https://en.wikipedia.org/wiki/Template:Cite", true},

		// Real article pages: allowed
		{"https://en.wikipedia.org/wiki/Category_theory", false}, // not Category: prefix
		{"https://en.wikipedia.org/wiki/Cat", false},
		{"https://en.wikipedia.org/wiki/URL", false},
		{"https://en.wikibooks.org/wiki/R_Programming", false},
		{"https://en.wikipedia.org/wiki/Special_relativity", false}, // underscore, not colon
		{"https://bartoszmilewski.com/2015/04/15/limits-and-colimits", false},
	}

	for _, tt := range tests {
		got := isMediaWikiMetaPage(tt.url)
		if got != tt.want {
			t.Errorf("isMediaWikiMetaPage(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestResolveBlocksWiktionary(t *testing.T) {
	// en.wiktionary.org should be blocked via frontier_blocked_domains.txt
	res := Resolve("https://en.wiktionary.org/wiki/ache")
	if res.Action != Block {
		t.Errorf("Resolve(en.wiktionary.org/wiki/ache) = %v, want Block", res.Action)
	}
}

func TestResolveBlocksFoundationWikimedia(t *testing.T) {
	res := Resolve("https://foundation.wikimedia.org/wiki/Policy:Terms_of_Use")
	if res.Action != Block {
		t.Errorf("Resolve(foundation.wikimedia.org) = %v, want Block", res.Action)
	}
}

func TestShouldBlockFrontierFromBlocksAllWikiCrosslinks(t *testing.T) {
	// English Wikipedia article shouldn't dump its language sidebar into the frontier.
	filter := ShouldBlockFrontierFrom("https://en.wikipedia.org/wiki/Cat")

	tests := []struct {
		target string
		want   bool
		desc   string
	}{
		{"https://en.wikipedia.org/wiki/Dog", true, "en→en (existing case)"},
		{"https://ps.wikipedia.org/wiki/Cat", true, "en→ps (the bug we fixed)"},
		{"https://de.wikipedia.org/wiki/Katze", true, "en→de (sidebar leak)"},
		{"https://en.wikibooks.org/wiki/Programming", false, "en wiki→en wikibooks (different project, smaller corpus, allow)"},
		{"https://bartoszmilewski.com/", false, "en wiki→external (allow)"},
	}

	for _, tt := range tests {
		got := filter(tt.target)
		if got != tt.want {
			t.Errorf("%s: filter(%q) = %v, want %v", tt.desc, tt.target, got, tt.want)
		}
	}
}
