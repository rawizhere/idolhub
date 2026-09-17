package cookies

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
// ParseNetscape parses a Netscape HTTP Cookie File (the format produced by
// the Cookie-Editor browser extension) into fhttp cookies. Lines look like:
//
//	#HttpOnly_.instagram.com\tTRUE\t/\tTRUE\t1801259143\tig_did\t9C49...
//
// Only cookies whose domain contains domainSuffix are returned; everything
// else is ignored.
func ParseNetscape(raw, domainSuffix string) ([]*fhttp.Cookie, error) {
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
		// Cookie-Editor writes empty values as a literal "" pair.
		if value == `""` {
			value = ""
		}
		if !strings.Contains(domain, domainSuffix) {
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
		return nil, fmt.Errorf("no %s cookies found in the netscape file", domainSuffix)
	}
	return cookies, nil
}
