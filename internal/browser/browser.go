// Package browser pairs TLS fingerprints with matching User-Agents.
package browser

import (
	"github.com/bogdanfinn/tls-client/profiles"
)

// Profile pairs a TLS fingerprint with a matching User-Agent.
type Profile struct {
	Profile profiles.ClientProfile
	UA      string
}

// FirefoxProfile is the one fixed Firefox profile used by every scraper.
var FirefoxProfile = Profile{
	Profile: profiles.Firefox_135,
	UA:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:135.0) Gecko/20100101 Firefox/135.0",
}
