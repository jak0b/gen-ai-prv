package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"
)

type fetchedResource struct {
	filename    string
	contentType string
	data        []byte
}

type resourceFetchFunc func(context.Context, string) (fetchedResource, error)

const maxResourceURLLength = 4096

var globallyRoutableIPv6 = netip.MustParsePrefix("2000::/3")

// fetchRemoteResource downloads a resource with safeguards against unsafe URLs
// and oversized or unexpected content. It accepts only HTTP(S) URLs on standard
// ports, rejects credentials and non-public IPs, and rechecks resolved addresses
// at dial time to prevent access to private networks. Redirects are limited and
// revalidated. Requests have a 30-second timeout, and response bodies are capped
// at 50 MiB. The response status, content type, filename, and file signature are
// validated before the data is returned.
func fetchRemoteResource(ctx context.Context, rawURL string) (result fetchedResource, fetchErr error) {
	started := time.Now()
	host := ""
	defer func() {
		if fetchErr != nil {
			log.Printf("Resource fetch: host=%q result=error duration=%s", host, time.Since(started).Round(time.Millisecond))
			return
		}
		log.Printf("Resource fetch: host=%q filename=%q content_type=%q bytes=%d duration=%s",
			host, result.filename, result.contentType, len(result.data), time.Since(started).Round(time.Millisecond))
	}()

	if len(rawURL) > maxResourceURLLength {
		return fetchedResource{}, fmt.Errorf("URL is too long")
	}
	parsed, err := validateResourceURL(rawURL)
	if err != nil {
		return fetchedResource{}, err
	}
	host = parsed.Hostname()

	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve resource host: %w", err)
			}
			var lastErr error
			for _, ip := range ips {
				if !isPublicResourceIP(ip) {
					return nil, fmt.Errorf("resource host resolves to a non-public address")
				}
				conn, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("resource host has no addresses")
			}
			return nil, lastErr
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("resource URL redirected too many times")
			}
			if _, err := validateResourceURL(req.URL.String()); err != nil {
				return fmt.Errorf("unsafe resource redirect: %w", err)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fetchedResource{}, fmt.Errorf("invalid resource URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fetchedResource{}, fmt.Errorf("could not download resource: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fetchedResource{}, fmt.Errorf("resource server returned %s", resp.Status)
	}
	if resp.ContentLength > maxFileSize {
		return fetchedResource{}, fmt.Errorf("resource is larger than the 50 MiB limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize+1))
	if err != nil {
		return fetchedResource{}, fmt.Errorf("could not read downloaded resource: %w", err)
	}
	if len(data) == 0 {
		return fetchedResource{}, fmt.Errorf("downloaded resource is empty")
	}
	if len(data) > maxFileSize {
		return fetchedResource{}, fmt.Errorf("resource is larger than the 50 MiB limit")
	}

	filename := resourceFilename(parsed)
	contentType, err := validateFetchedResource(filename, resp.Header.Get("Content-Type"), data)
	if err != nil {
		return fetchedResource{}, err
	}
	return fetchedResource{filename: filename, contentType: contentType, data: data}, nil
}

func validateResourceURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return nil, fmt.Errorf("enter a valid HTTP or HTTPS URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only HTTP and HTTPS URLs are supported")
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("enter a valid HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("URLs containing credentials are not supported")
	}
	if parsed.Opaque != "" {
		return nil, fmt.Errorf("enter a valid HTTP or HTTPS URL")
	}
	parsed.Fragment = ""
	port := parsed.Port()
	if port != "" && (parsed.Scheme == "http" && port != "80" || parsed.Scheme == "https" && port != "443") {
		return nil, fmt.Errorf("only standard HTTP and HTTPS ports are supported")
	}
	if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil && !isPublicResourceIP(ip) {
		return nil, fmt.Errorf("URLs pointing to private or local network addresses are not allowed")
	}
	return parsed, nil
}

func isPublicResourceIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is6() && !globallyRoutableIPv6.Contains(ip) {
		return false
	}
	blocked := []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, prefix := range blocked {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func resourceFilename(resourceURL *url.URL) string {
	name, err := url.PathUnescape(path.Base(strings.TrimSuffix(resourceURL.EscapedPath(), "/")))
	if err != nil || name == "." || name == "/" || name == "" {
		name = resourceURL.Hostname()
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if name == "" {
		return "resource"
	}
	return name
}

func validateFetchedResource(filename, headerContentType string, data []byte) (string, error) {
	expected, err := contentTypeFor(filename)
	if err != nil {
		return "", err
	}
	declared, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(headerContentType)), ";")
	declared = strings.TrimSpace(declared)
	if declared != "" && declared != "application/octet-stream" && !compatibleContentType(expected, declared) {
		return "", fmt.Errorf("downloaded content type %q does not match the file format %s", declared, expected)
	}
	if !hasValidResourceSignature(filename, data) {
		return "", fmt.Errorf("downloaded data is not a valid %s resource", strings.TrimPrefix(expected, "application/"))
	}
	return expected, nil
}

func compatibleContentType(expected, declared string) bool {
	if expected == declared {
		return true
	}
	switch expected {
	case "image/jpeg":
		return declared == "image/jpg"
	case "audio/wav":
		return declared == "audio/x-wav" || declared == "audio/wave"
	case "audio/ogg":
		return declared == "application/ogg" || declared == "audio/opus"
	case "audio/aac":
		return declared == "audio/x-aac"
	case "audio/pcm":
		return declared == "audio/L16" || declared == "audio/l16"
	}
	return false
}

func hasValidResourceSignature(filename string, data []byte) bool {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff
	case strings.HasSuffix(lower, ".webp"):
		return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
	case strings.HasSuffix(lower, ".gif"):
		return len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a")
	case strings.HasSuffix(lower, ".mp3"):
		return len(data) >= 3 && (string(data[:3]) == "ID3" || data[0] == 0xff && data[1]&0xe0 == 0xe0)
	case strings.HasSuffix(lower, ".opus"):
		return len(data) >= 4 && string(data[:4]) == "OggS"
	case strings.HasSuffix(lower, ".aac"):
		return len(data) >= 2 && data[0] == 0xff && data[1]&0xf6 == 0xf0
	case strings.HasSuffix(lower, ".flac"):
		return len(data) >= 4 && string(data[:4]) == "fLaC"
	case strings.HasSuffix(lower, ".wav"):
		return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE"
	case strings.HasSuffix(lower, ".pdf"):
		return len(data) >= 5 && string(data[:5]) == "%PDF-"
	case strings.HasSuffix(lower, ".pcm"):
		return len(data) > 0
	default:
		return false
	}
}
