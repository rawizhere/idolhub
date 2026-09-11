// Package browser pairs TLS fingerprints with matching User-Agents so
// scraping clients look like one coherent browser build.
package browser

import (
	"math/rand"

	"github.com/bogdanfinn/tls-client/profiles"
)

// Profile pairs a TLS fingerprint with a matching User-Agent.
type Profile struct {
	Profile profiles.ClientProfile
	UA      string
}

var allProfiles = []Profile{
	{profiles.Chrome_131, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	{profiles.Chrome_133, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"},
	{profiles.Chrome_146, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"},
	{profiles.Chrome_152, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"},
	{profiles.Firefox_135, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:135.0) Gecko/20100101 Firefox/135.0"},
	{profiles.Firefox_148, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:148.0) Gecko/20100101 Firefox/148.0"},
}

// Random returns a random coherent TLS profile + UA pair.
func Random() Profile {
	return allProfiles[rand.Intn(len(allProfiles))]
}
