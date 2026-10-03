package fsx

import "errors"

// Ошибки, которые вызывающий код проверяет через errors.Is.
var (
	// ErrBusy — файл держит открытым другой процесс, повторы не помогли.
	ErrBusy = errors.New("файл занят другим процессом")
	// ErrUnsafePath — относительный путь выходит за пределы папки или содержит недопустимое имя.
	ErrUnsafePath = errors.New("небезопасный путь")
	// ErrCrossVolume — жёсткая ссылка невозможна: источник и назначение на разных томах.
	ErrCrossVolume = errors.New("файлы на разных томах")
	// ErrLinkUnsupported — файловая система не умеет жёсткие ссылки (exFAT, FAT32, часть сетевых дисков).
	ErrLinkUnsupported = errors.New("файловая система не поддерживает жёсткие ссылки")
	// ErrTrashUnavailable — перенести в Корзину нельзя; файл остался на месте.
	ErrTrashUnavailable = errors.New("Корзина недоступна")
)
