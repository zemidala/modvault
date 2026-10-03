//go:build !windows

package nexus

import "github.com/zemidala/modvault/i18n"

var errUnsupported = i18n.NewError("поддерживается только в Windows")

type credential struct{ target string }

func (credential) Load() (string, error) { return "", nil }
func (credential) Save(string) error     { return errUnsupported }
func (credential) Delete() error         { return nil }

type protocol struct{ path string }

func (protocol) Command() (string, error) { return "", nil }
func (protocol) SetCommand(string) error  { return errUnsupported }
