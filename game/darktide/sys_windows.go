package darktide

import (
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func steamRoot() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	path, _, err := k.GetStringValue("SteamPath")
	if err != nil {
		return ""
	}
	return path
}

// steamRunning сообщает, запущен ли Steam: он записывает номер своего
// процесса в реестр и обнуляет его при выходе.
func steamRunning() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam\ActiveProcess`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	pid, _, err := k.GetIntegerValue("pid")
	return err == nil && pid != 0
}

func drives() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<i) != 0 {
			out = append(out, string(rune('A'+i))+`:\`)
		}
	}
	return out
}

// openURL открывает адрес программой, которая за ним закреплена.
func openURL(url string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

// hiddenWindow не даёт консольному патчеру мигнуть окном.
func hiddenWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
