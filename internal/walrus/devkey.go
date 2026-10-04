package walrus

import (
	"net"
	"net/url"
	"strings"
)

// DevKey is the key a development engine uses when it is started without WALRUS_ADMIN_KEY. The
// engine accepts it only on a loopback address, so it is only ever assumed for one.
const DevKey = "key"

// KeyFor returns key, or the development key when key is empty and baseURL points at this machine.
// For any other engine it returns key as it is, empty or not.
func KeyFor(baseURL, key string) string {
	if key != "" || !isLocal(baseURL) {
		return key
	}
	return DevKey
}

func isLocal(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
