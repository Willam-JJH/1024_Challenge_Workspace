package api

import (
	"net"
	"testing"
)

func TestIsNonPublicIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       true,
		"10.1.2.3":        true,
		"192.168.1.10":    true,
		"172.20.0.1":      true,
		"169.254.1.1":     true,
		"198.18.1.47":     true, // Clash fake-ip 网段，Python 视为 private
		"192.0.2.5":       true,
		"0.0.0.0":         true,
		"100.64.0.1":      false, // Python 不把共享地址空间算作 private
		"93.184.216.34":   false,
		"2606:4700::1111": false,
		"::1":             true,
		"fd00::1":         true,
		"fe80::1":         true,
	}
	for raw, want := range cases {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("bad test ip %s", raw)
		}
		if got := isNonPublicIP(ip); got != want {
			t.Errorf("isNonPublicIP(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestExtractPreview(t *testing.T) {
	html := `<html><head>
		<title>  Fallback Title  </title>
		<meta property="og:description" content="desc here">
		<meta property='og:image' content='https://cdn.example/x.png'>
	</head></html>`
	title, description, image := extractPreview(html)
	if title != "  Fallback Title  " {
		t.Errorf("title = %q", title)
	}
	if description != "desc here" {
		t.Errorf("description = %q", description)
	}
	if image != "https://cdn.example/x.png" {
		t.Errorf("image = %q", image)
	}

	og := `<meta property="og:title" content="OG Title"><title>other</title>`
	if title, _, _ = extractPreview(og); title != "OG Title" {
		t.Errorf("og title = %q", title)
	}
}
