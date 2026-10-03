package nexus

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	advapi32    = windows.NewLazySystemDLL("advapi32.dll")
	credWriteW  = advapi32.NewProc("CredWriteW")
	credReadW   = advapi32.NewProc("CredReadW")
	credDeleteW = advapi32.NewProc("CredDeleteW")
	credFree    = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

// credentialW — CREDENTIALW из wincred.h.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// credential — запись в хранилище учётных данных Windows.
type credential struct{ target string }

func (c credential) Load() (string, error) {
	target, err := windows.UTF16PtrFromString(c.target)
	if err != nil {
		return "", err
	}
	var cred *credentialW
	ok, _, err := credReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&cred)))
	if ok == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return "", nil
		}
		return "", err
	}
	defer credFree.Call(uintptr(unsafe.Pointer(cred)))
	if cred.CredentialBlobSize == 0 {
		return "", nil
	}
	return string(unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)), nil
}

func (c credential) Save(key string) error {
	if key == "" {
		return c.Delete()
	}
	target, err := windows.UTF16PtrFromString(c.target)
	if err != nil {
		return err
	}
	user, _ := windows.UTF16PtrFromString("Nexus Mods")
	blob := []byte(key)
	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           user,
	}
	ok, _, err := credWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if ok == 0 {
		return err
	}
	return nil
}

func (c credential) Delete() error {
	target, err := windows.UTF16PtrFromString(c.target)
	if err != nil {
		return err
	}
	ok, _, err := credDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0)
	if ok == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return err
	}
	return nil
}

// protocol — обработчик ссылок в реестре текущего пользователя; path —
// ключ протокола относительно HKEY_CURRENT_USER.
type protocol struct{ path string }

const commandKey = `\shell\open\command`

func (p protocol) Command() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, p.path+commandKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer k.Close()
	cmd, _, err := k.GetStringValue("")
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return cmd, err
}

func (p protocol) SetCommand(command string) error {
	if command == "" {
		// Убираем только команду: остальной ключ мог завести кто-то другой.
		err := registry.DeleteKey(registry.CURRENT_USER, p.path+commandKey)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	root, _, err := registry.CreateKey(registry.CURRENT_USER, p.path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer root.Close()
	// Без этих двух значений Windows не считает ключ протоколом.
	if err := root.SetStringValue("", "URL:NXM Protocol"); err != nil {
		return err
	}
	if err := root.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, p.path+commandKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("", command)
}
