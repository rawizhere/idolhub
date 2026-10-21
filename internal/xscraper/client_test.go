package xscraper

import "testing"

// An export holding only auth_token (what migration 00005 makes of a bare token) must yield the header the bare token did.
func TestNewXClientCookieHeader(t *testing.T) {
	for _, export := range []string{
		".x.com\tTRUE\t/\tTRUE\t2147483647\tauth_token\tTOK\n",
		".x.com\tTRUE\t/\tTRUE\t2147483647\tauth_token\tTOK\n.x.com\tTRUE\t/\tTRUE\t2147483647\tct0\told\n",
	} {
		c, err := newXClient("CSRF", export)
		if err != nil {
			t.Fatal(err)
		}
		if c.cookieHdr != "auth_token=TOK; ct0=CSRF" || c.csrfToken != "CSRF" {
			t.Errorf("cookie header = %q, csrf = %q", c.cookieHdr, c.csrfToken)
		}
	}
	if _, err := newXClient("CSRF", ".x.com\tTRUE\t/\tTRUE\t1\tkdt\tk\n"); err == nil {
		t.Error("export without auth_token accepted")
	}
}
