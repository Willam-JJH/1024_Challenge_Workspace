package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	linkPreviewPattern = "POST /api/v1/link-previews"
	githubRepoPattern  = "GET /api/v1/github/repos/{owner}/{repo}"
)

var (
	titleTagRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	ogTitleRe  = regexp.MustCompile(`(?i)property=["']og:title["']\s+content=["']([^"']+)`)
	ogDescRe   = regexp.MustCompile(`(?i)property=["']og:description["']\s+content=["']([^"']+)`)
	ogImageRe  = regexp.MustCompile(`(?i)property=["']og:image["']\s+content=["']([^"']+)`)
)

var previewClient = &http.Client{Timeout: 8 * time.Second}

type linkPreviewIn struct {
	URL *string `json:"url"`
}

type linkPreviewOut struct {
	URL         string  `json:"url"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Image       *string `json:"image"`
}

// pythonPrivateNetworks 与 Python ipaddress 的 is_private 集合保持一致
// （含文档/基准网段；100.64/10 在 Python 里不算 private，这里也不拦）。
var pythonPrivateNetworks = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32",
		"fc00::/7", "fe80::/10",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			out = append(out, block)
		}
	}
	return out
}()

func isNonPublicIP(address net.IP) bool {
	if address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsUnspecified() {
		return true
	}
	for _, block := range pythonPrivateNetworks {
		if block.Contains(address) {
			return true
		}
	}
	return false
}

// assertPublicHost 复刻 Python 的 _assert_public_host：只允许解析到公网地址的 http(s) 链接。
func assertPublicHost(rawURL string) *APIError {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" {
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "invalid_url",
			Message: "只接受 http/https 链接"}
	}
	addresses, err := net.LookupIP(parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "invalid_url",
			Message: "域名解析失败"}
	}
	for _, address := range addresses {
		if isNonPublicIP(address) {
			return &APIError{Status: http.StatusForbidden, Code: "forbidden",
				Message: "不能预览内网地址"}
		}
	}
	return nil
}

func firstMatch(re *regexp.Regexp, text string) string {
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func optionalText(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func truncateText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// extractPreview 复刻 Python 的取值顺序：og:* 优先，标题回退到 <title>。
func extractPreview(html string) (string, string, string) {
	title := firstMatch(ogTitleRe, html)
	if title == "" {
		title = firstMatch(titleTagRe, html)
	}
	return title, firstMatch(ogDescRe, html), firstMatch(ogImageRe, html)
}

func (s *Server) linkPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	var in linkPreviewIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.URL == nil || len(*in.URL) < 4 || len(*in.URL) > 500 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if problem := assertPublicHost(*in.URL); problem != nil {
		WriteError(w, r, problem)
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, *in.URL, nil)
	if err != nil {
		WriteError(w, r, &APIError{Status: http.StatusUnprocessableEntity, Code: "invalid_url",
			Message: "只接受 http/https 链接"})
		return
	}
	request.Header.Set("User-Agent", "1024-challenge/0.1")
	response, err := previewClient.Do(request)
	if err != nil {
		WriteError(w, r, &APIError{Status: http.StatusBadGateway, Code: "fetch_failed",
			Message: "抓取失败"})
		return
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	_ = response.Body.Close()
	if readErr != nil {
		WriteError(w, r, &APIError{Status: http.StatusBadGateway, Code: "fetch_failed",
			Message: "抓取失败"})
		return
	}
	html := string(body)
	if !utf8.ValidString(html) {
		html = strings.ToValidUTF8(html, "�")
	}
	html = truncateText(html, 200_000)

	titleText, descriptionText, imageText := extractPreview(html)
	finalURL := *in.URL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}

	out := linkPreviewOut{URL: finalURL}
	if text := optionalText(truncateText(titleText, 200)); text != nil {
		out.Title = text
	}
	if text := optionalText(truncateText(descriptionText, 300)); text != nil {
		out.Description = text
	}
	if text := optionalText(imageText); text != nil {
		out.Image = text
	}
	writeJSON(w, http.StatusOK, out)
}

type githubRepoOut struct {
	FullName    string  `json:"full_name"`
	Description *string `json:"description"`
	Stars       int     `json:"stars"`
	Language    *string `json:"language"`
	URL         string  `json:"url"`
}

func (s *Server) githubRepo(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	owner := r.PathValue("owner")
	repo := r.PathValue("repo")
	target := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "1024-challenge/0.1")
	response, err := previewClient.Do(request)
	if err != nil {
		WriteError(w, r, &APIError{Status: http.StatusBadGateway, Code: "fetch_failed",
			Message: "GitHub 请求失败"})
		return
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	_ = response.Body.Close()
	if readErr != nil {
		WriteError(w, r, &APIError{Status: http.StatusBadGateway, Code: "fetch_failed",
			Message: "GitHub 请求失败"})
		return
	}
	if response.StatusCode == http.StatusNotFound {
		writeError(w, r, http.StatusNotFound, "not_found", "仓库不存在或不是公开仓库")
		return
	}
	if response.StatusCode >= 400 {
		WriteError(w, r, &APIError{Status: http.StatusBadGateway, Code: "fetch_failed",
			Message: "GitHub 返回错误"})
		return
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		WriteError(w, r, errInternal)
		return
	}
	fallback := owner + "/" + repo
	fullName, _ := data["full_name"].(string)
	if fullName == "" {
		fullName = fallback
	}
	htmlURL, _ := data["html_url"].(string)
	if htmlURL == "" {
		htmlURL = "https://github.com/" + fallback
	}
	out := githubRepoOut{FullName: fullName, URL: htmlURL}
	if description, ok := data["description"].(string); ok && description != "" {
		out.Description = &description
	}
	if language, ok := data["language"].(string); ok && language != "" {
		out.Language = &language
	}
	out.Stars = pyInt(data["stargazers_count"])
	writeJSON(w, http.StatusOK, out)
}
