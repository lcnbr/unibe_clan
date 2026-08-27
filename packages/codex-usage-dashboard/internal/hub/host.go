package hub

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

type allowedHostSet map[string]struct{}

func newAllowedHostSet(values []string) (allowedHostSet, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one allowed HTTP host is required")
	}

	hosts := make(allowedHostSet, len(values))
	for _, value := range values {
		host, hasPort, err := canonicalAuthority(value)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed HTTP host %q: %w", value, err)
		}
		if hasPort {
			return nil, fmt.Errorf("invalid allowed HTTP host %q: ports are not allowed", value)
		}
		hosts[host] = struct{}{}
	}
	return hosts, nil
}

func (hosts allowedHostSet) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := canonicalAuthority(r.Host)
		if err != nil {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		if _, ok := hosts[host]; !ok {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// canonicalAuthority parses an HTTP Host value, returning its canonical host
// and whether it included a port. It deliberately does not resolve names.
func canonicalAuthority(authority string) (string, bool, error) {
	if authority == "" {
		return "", false, errors.New("host is empty")
	}
	for _, char := range authority {
		if char <= ' ' || char == 0x7f {
			return "", false, errors.New("host contains whitespace or a control character")
		}
	}
	if strings.ContainsAny(authority, "/\\?#@") {
		return "", false, errors.New("host contains an invalid delimiter")
	}

	host := authority
	port := ""
	hasPort := false

	if strings.HasPrefix(authority, "[") {
		closing := strings.IndexByte(authority, ']')
		if closing < 0 {
			return "", false, errors.New("IPv6 literal is missing a closing bracket")
		}
		host = authority[1:closing]
		rest := authority[closing+1:]
		switch {
		case rest == "":
		case strings.HasPrefix(rest, ":") && len(rest) > 1:
			hasPort = true
			port = rest[1:]
		default:
			return "", false, errors.New("invalid text after bracketed IPv6 literal")
		}
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() {
			return "", false, errors.New("brackets require an IPv6 literal")
		}
	} else if _, err := netip.ParseAddr(authority); err == nil {
		// A bare IP literal has no port. In particular, an IPv6 address must be
		// bracketed before a port can be appended.
	} else {
		switch strings.Count(authority, ":") {
		case 0:
		case 1:
			host, port, _ = strings.Cut(authority, ":")
			if host == "" || port == "" {
				return "", false, errors.New("host or port is empty")
			}
			hasPort = true
		default:
			return "", false, errors.New("IPv6 literals with ports must use brackets")
		}
	}

	if hasPort {
		if err := validateAuthorityPort(port); err != nil {
			return "", false, err
		}
	}
	canonical, err := canonicalHost(host)
	if err != nil {
		return "", false, err
	}
	return canonical, hasPort, nil
}

func validateAuthorityPort(port string) error {
	if len(port) == 0 || len(port) > 5 {
		return errors.New("port must be between 1 and 65535")
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return errors.New("port must be numeric")
		}
	}
	numeric, err := strconv.ParseUint(port, 10, 16)
	if err != nil || numeric == 0 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

func canonicalHost(host string) (string, error) {
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", errors.New("host is empty")
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Zone() != "" {
			return "", errors.New("scoped IP literals are not allowed")
		}
		return address.Unmap().String(), nil
	}

	if len(host) > 253 {
		return "", errors.New("DNS name is too long")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", errors.New("DNS label length is invalid")
		}
		if !asciiLetterOrDigit(label[0]) || !asciiLetterOrDigit(label[len(label)-1]) {
			return "", errors.New("DNS labels must start and end with a letter or digit")
		}
		for index := 1; index < len(label)-1; index++ {
			if !asciiLetterOrDigit(label[index]) && label[index] != '-' {
				return "", errors.New("DNS label contains an invalid character")
			}
		}
	}
	return strings.ToLower(host), nil
}

func asciiLetterOrDigit(char byte) bool {
	return char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9'
}
