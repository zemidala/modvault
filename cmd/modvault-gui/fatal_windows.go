package main

import (
	"os"

	"golang.org/x/sys/windows"

	"github.com/zemidala/modvault/i18n"
)

// fatal показывает ошибку запуска окном: у программы нет консоли, и иначе
// пользователь о сбое не узнает.
func fatal(err error) {
	text, _ := windows.UTF16PtrFromString(i18n.T("Не удалось открыть окно программы.\n\n") + err.Error())
	title, _ := windows.UTF16PtrFromString("Modvault")
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(1)
}
