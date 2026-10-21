// Package browser is the one browser identity every scraper presents: a Firefox TLS fingerprint with its User-Agent.
package browser

import "github.com/bogdanfinn/tls-client/profiles"

// TLS is the Firefox fingerprint; UA must stay the User-Agent of that same Firefox, CDN downloads included.
var TLS = profiles.Firefox_148

const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:148.0) Gecko/20100101 Firefox/148.0"
