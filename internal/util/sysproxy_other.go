//go:build !windows

package util

import (
	"net/http"
	"net/url"
)

func SmartProxyFunc() func(*http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment
}
