package fsx

import "github.com/zemidala/modvault/i18n"

// Ошибки, которые вызывающий код проверяет через errors.Is.
var (
	// ErrBusy — файл держит открытым другой процесс, повторы не помогли.
	ErrBusy = i18n.NewError("файл занят другим процессом")
	// ErrUnsafePath — относительный путь выходит за пределы папки или содержит недопустимое имя.
	ErrUnsafePath = i18n.NewError("небезопасный путь")
	// ErrCrossVolume — жёсткая ссылка невозможна: источник и назначение на разных томах.
	ErrCrossVolume = i18n.NewError("файлы на разных томах")
	// ErrLinkUnsupported — файловая система не умеет жёсткие ссылки (exFAT, FAT32, часть сетевых дисков).
	ErrLinkUnsupported = i18n.NewError("файловая система не поддерживает жёсткие ссылки")
	// ErrTrashUnavailable — перенести в Корзину нельзя; файл остался на месте.
	ErrTrashUnavailable = i18n.NewError("Корзина недоступна")
)
