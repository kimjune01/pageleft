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
