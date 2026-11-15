package deliverytargets

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// maxHostLength is the longest host (with a port) accepted.
const maxHostLength = 256

// NormalizeHost turns what an author typed into the form the server keeps in a
// remote user's host: lower case, IDN in punycode, a port only when given.
//
// URL (`https://remote.example/@user`) を貼られても、ホストの部分だけを取る。
// 比べるのは完全一致なので、ここで形をそろえないと、外したつもりのホストが
// 表記の違いで外れない。
func NormalizeHost(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("host %q is not a host name", s)
		}
		s = u.Host
	}
	s = strings.TrimPrefix(s, "@")
	if s == "" || len(s) > maxHostLength {
		return "", fmt.Errorf("host %q is empty or too long", s)
	}
	name, port := s, ""
	if h, p, err := net.SplitHostPort(s); err == nil {
		name, port = h, p
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("host %q has an invalid port", s)
		}
	}
	name = strings.TrimSuffix(name, ".")
	ascii, err := idna.Lookup.ToASCII(strings.ToLower(name))
	if err != nil || ascii == "" || strings.ContainsAny(ascii, "/@?#[]:") {
		return "", fmt.Errorf("host %q is not a host name", s)
	}
	if port != "" {
		return ascii + ":" + port, nil
	}
	return ascii, nil
}
