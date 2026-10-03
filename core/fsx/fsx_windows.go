package fsx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Антивирус и индексатор держат свежий файл доли секунды, поэтому занятость
// сначала пережидаем. В сумме около 0,3 с.
var retryDelays = []time.Duration{
	10 * time.Millisecond,
	20 * time.Millisecond,
	40 * time.Millisecond,
	80 * time.Millisecond,
	160 * time.Millisecond,
}

// withRetry выполняет op, повторяя её, пока файл занят. Если повторы
// не помогли и виноват другой процесс, ошибка оборачивается в ErrBusy.
// target — путь, чью занятость проверяем при «отказано в доступе».
func withRetry(target string, op func() error) error {
	var err error
	for i := 0; ; i++ {
		err = op()
		if err == nil || !isSharing(err) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return err
		}
		if i == len(retryDelays) {
			break
		}
		time.Sleep(retryDelays[i])
	}
	// Замена открытого файла даёт «отказано в доступе», как и нехватка прав.
	// Отличаем одно от другого прямой проверкой.
	if isSharing(err) || heldOpen(target) {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return err
}

func isSharing(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// heldOpen сообщает, держит ли кто-то файл открытым так, что его нельзя
// заменить или удалить. Сам файл не меняется.
func heldOpen(path string) bool {
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return isSharing(err)
	}
	windows.CloseHandle(h)
	return false
}

// longPath добавляет префикс, снимающий предел в 260 символов для прямых
// вызовов Windows API. Пакет os делает это сам.
func longPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	switch {
	case strings.HasPrefix(abs, `\\?\`):
		return abs
	case strings.HasPrefix(abs, `\\`):
		return `\\?\UNC\` + abs[2:]
	}
	return `\\?\` + abs
}

// syncDir в Windows не нужен: папку нельзя сбросить на диск отдельно,
// переименование записывается в журнал NTFS.
func syncDir(string) error {
	return nil
}

// volumeID возвращает серийный номер тома. Открытие через os раскрывает
// точки соединения, так что папка-ссылка на другой диск определяется верно.
func volumeID(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return 0, &os.PathError{Op: "GetFileInformationByHandle", Path: path, Err: err}
	}
	return uint64(info.VolumeSerialNumber), nil
}

func linkError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_NOT_SAME_DEVICE):
		return fmt.Errorf("%w: %w", ErrCrossVolume, err)
	case errors.Is(err, windows.ERROR_INVALID_FUNCTION), errors.Is(err, windows.ERROR_NOT_SUPPORTED):
		return fmt.Errorf("%w: %w", ErrLinkUnsupported, err)
	}
	return err
}
