package crawler

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func parse(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"basic", "<html><head><title>Hello</title></head></html>", "Hello"},
		{"whitespace", "<html><head><title>  spaced  </title></head></html>", "spaced"},
		{"missing", "<html><head></head></html>", ""},
		{"raw text in title", "<html><head><title>A <b>bold</b> title</title></head></html>", "A <b>bold</b> title"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTitle(parse(t, tt.html))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"basic", "<html><body><p>Hello world</p></body></html>", "Hello world"},
		{"skips script", "<html><body><p>Keep</p><script>drop()</script></body></html>", "Keep"},
		{"skips style", "<html><body><p>Keep</p><style>.drop{}</style></body></html>", "Keep"},
		{"skips nav", "<html><body><nav>Menu</nav><p>Content</p></body></html>", "Content"},
		{"normalizes whitespace", "<html><body><p>  lots   of   space  </p></body></html>", "lots of space"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractText(parse(t, tt.html))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirst500Words(t *testing.T) {
	short := "one two three"
	if got := First500Words(short); got != short {
		t.Errorf("short: got %q, want %q", got, short)
	}

	words := make([]string, 600)
	for i := range words {
		words[i] = "word"
	}
	long := strings.Join(words, " ")
	got := First500Words(long)
	if n := len(strings.Fields(got)); n != 500 {
		t.Errorf("long: got %d words, want 500", n)
	}
}

func TestCheckDomain(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantLicense bool
		wantBlocked bool
		wantType    string
	}{
		{
			"copyleft domain",
			"https://bartoszmilewski.com/2014/11/04/category-the-essence-of-composition/",
			true, false, "CC BY-SA",
		},
		{
			"copyleft domain with www",
			"https://www.bartoszmilewski.com/some/page",
			true, false, "CC BY-SA",
		},
		{
			"blocked domain medium",
			"https://medium.com/@someone/some-post-123",
			false, true, "",
		},
		{
			"blocked domain lesswrong",
			"https://lesswrong.com/posts/abc/some-post",
			false, true, "",
		},
		{
			"blocked domain substack subdomain",
			"https://someone.substack.com/p/some-post",
			false, true, "",
		},
		{
			"unknown domain falls through",
			"https://example.com/page",
			false, false, "",
		},
		{
			"wikipedia copyleft",
			"https://en.wikipedia.org/wiki/Category_theory",
			true, false, "CC BY-SA",
		},
		{
			"rfc-editor public domain",
			"https://www.rfc-editor.org/rfc/rfc2616",
			true, false, "Public Domain",
		},
		{
			"gutenberg public domain",
			"https://www.gutenberg.org/ebooks/5740",
			true, false, "Public Domain",
		},
		{
			"nasa public domain",
			"https://ntrs.nasa.gov/citations/19880069935",
			true, false, "Public Domain",
		},
		{
			"nist public domain",
			"https://www.nist.gov/publications/some-report",
			true, false, "Public Domain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			license, blocked, _ := CheckDomain(tt.url)
			if blocked != tt.wantBlocked {
				t.Errorf("blocked: got %v, want %v", blocked, tt.wantBlocked)
			}
			if tt.wantLicense {
				if license == nil {
					t.Fatal("expected license, got nil")
				}
				if license.Type != tt.wantType {
					t.Errorf("type: got %q, want %q", license.Type, tt.wantType)
				}
			} else if license != nil && !blocked {
				t.Errorf("expected nil license, got %+v", license)
			}
		})
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://bartoszmilewski.com/2014/page", "bartoszmilewski.com"},
		{"https://www.bartoszmilewski.com/page", "bartoszmilewski.com"},
		{"https://en.wikipedia.org/wiki/Thing", "en.wikipedia.org"},
		{"https://medium.com/@user/post", "medium.com"},
		{"not a url", ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := ExtractDomain(tt.url)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectLicense(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		wantNil bool
		wantTyp string
	}{
		{
			"link rel license CC BY-SA",
			`<html><head><link rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/"></head></html>`,
			false, "CC BY-SA",
		},
		{
			"anchor rel license GPL",
			`<html><body><a rel="license" href="https://www.gnu.org/licenses/gpl-3.0.html">GPL</a></body></html>`,
			false, "GPL",
		},
		{
			"no license",
			`<html><body><p>No license here</p></body></html>`,
			true, "",
		},
		{
			"MIT not copyleft",
			`<html><head><link rel="license" href="https://opensource.org/licenses/MIT"></head></html>`,
			true, "",
		},
		{
			"CC0 public domain",
			`<html><head><link rel="license" href="https://creativecommons.org/publicdomain/zero/1.0/"></head></html>`,
			false, "CC0",
		},
		{
			"public domain mark",
			`<html><body><a rel="license" href="https://creativecommons.org/publicdomain/mark/1.0/">Public Domain</a></body></html>`,
			false, "Public Domain",
		},
		{
			"unlicense",
			`<html><head><link rel="license" href="https://unlicense.org/"></head></html>`,
			false, "Unlicense",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectLicense(parse(t, tt.html))
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected license, got nil")
			}
			if got.Type != tt.wantTyp {
				t.Errorf("got type %q, want %q", got.Type, tt.wantTyp)
			}
		})
	}
}
