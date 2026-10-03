//go:build !windows

package vortex

import "github.com/zemidala/modvault/i18n"

func hardLinks(string) ([]string, error) {
	return nil, i18n.NewError("поиск жёстких ссылок поддерживается только в Windows")
}
