// expected_domain check. Each item's URL must be https + host equal
// or sub-domain of the configured expected_domain. Mismatch → reject
// the entire snapshot (defense-in-depth: a single rogue link in an
// otherwise-correct payload still triggers rejection because we
// can't tell which item is the canary).

package rankings

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	ErrInsecureURL  = errors.New("rankings: non-https url")
	ErrDomainMismatch = errors.New("rankings: url host outside expected_domain")
)

// ValidateSnapshot returns nil when every item passes; on first
// failure returns an error describing the bad URL. Empty
// expectedDomain skips the check entirely (legitimate for boards
// where we explicitly trust upstream; not the default).
func ValidateSnapshot(snap *Snapshot, expectedDomain string) error {
	if expectedDomain == "" {
		return nil
	}
	for i, it := range snap.Items {
		if it.URL == "" {
			continue
		}
		if err := validateURL(it.URL, expectedDomain); err != nil {
			return fmt.Errorf("item[%d] %q: %w", i, it.Title, err)
		}
	}
	return nil
}

func validateURL(rawURL, expectedDomain string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("rankings: bad url %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: %s", ErrInsecureURL, rawURL)
	}
	host := strings.ToLower(u.Hostname())
	want := strings.ToLower(expectedDomain)
	// Accept exact match or sub-domain (.example.com).
	if host == want || strings.HasSuffix(host, "."+want) {
		return nil
	}
	return fmt.Errorf("%w: %s not in %s", ErrDomainMismatch, host, expectedDomain)
}
