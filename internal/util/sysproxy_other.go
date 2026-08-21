//go:build !windows

package util

import (
	"net/http"
	"net/url"
)

// SmartProxyFunc is the non-Windows alias of the Windows system-proxy
// resolver (sysproxy_windows.go): everywhere else the environment
// variables remain the only proxy source, which is exactly the previous
// behavior of http.ProxyFromEnvironment.
func SmartProxyFunc() func(*http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment
}
