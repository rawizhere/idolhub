package scraper

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

// parseNetscapeCookies parses a Netscape HTTP Cookie File (the format
// produced by the Cookie-Editor browser extension) into fhttp cookies.
// Lines look like:
//
//	#HttpOnly_.instagram.com	TRUE	/	TRUE	1801259143	ig_did	9C49...
//
// Only instagram.com cookies are returned; everything else is ignored.
func parseNetscapeCookies(raw string) ([]*fhttp.Cookie, error) {
	var cookies []*fhttp.Cookie
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\n")
		if line == "" || line == `""` {
			continue
		}
		httpOnly := false
		if strings.HasPrefix(line, "#HttpOnly_") {
			httpOnly = true
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 8)
		if len(parts) < 7 {
			return nil, fmt.Errorf("malformed netscape cookie line: %q", line)
		}
		domain := parts[0]
		path := parts[2]
		secure := parts[3]
		expires := parts[4]
		name := parts[5]
		value := strings.Join(parts[6:], "\t")
		if !strings.Contains(domain, "instagram.com") {
			continue
		}
		exp, err := strconv.ParseInt(expires, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad cookie expiry for %s: %w", name, err)
		}
		cookies = append(cookies, &fhttp.Cookie{
			Name:     name,
			Value:    value,
			Domain:   domain,
			Path:     path,
			Secure:   secure == "TRUE",
			HttpOnly: httpOnly,
			Expires:  time.Unix(exp, 0),
		})
	}
	if len(cookies) == 0 {
		return nil, fmt.Errorf("no instagram.com cookies found in the netscape file")
	}
	return cookies, nil
}
