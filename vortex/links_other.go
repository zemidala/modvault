//go:build !windows

package vortex

import "errors"

func hardLinks(string) ([]string, error) {
	return nil, errors.New("поиск жёстких ссылок поддерживается только в Windows")
}
