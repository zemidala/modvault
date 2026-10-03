package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/zemidala/modvault/i18n"
)

// Корзина работает через COM-интерфейс IFileOperation. Старая функция
// SHFileOperation не годится: если Корзина для диска недоступна, она без
// вопросов удаляет насовсем. IFileOperation перед каждым удалением
// спрашивает наш обработчик, и тот отменяет всё, что не попадёт в Корзину.

var (
	modole32   = windows.NewLazySystemDLL("ole32.dll")
	modshell32 = windows.NewLazySystemDLL("shell32.dll")

	procCoCreateInstance            = modole32.NewProc("CoCreateInstance")
	procSHCreateItemFromParsingName = modshell32.NewProc("SHCreateItemFromParsingName")
)

var (
	clsidFileOperation = windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	iidFileOperation   = windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	iidProgressSink    = windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}
	iidShellItem       = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	iidUnknown         = windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
)

const (
	clsctxAll = 0x17

	// Без окон и вопросов; удалять только в Корзину; остановиться на первой ошибке.
	fofAllowUndo        = 0x40
	fofNoUI             = 0x4 | 0x10 | 0x200 | 0x400
	fofxRecycleOnDelete = 0x00080000
	fofxEarlyFailure    = 0x00100000

	// Флаг в PreDeleteItem: элемент уйдёт в Корзину, а не будет стёрт.
	tsfDeleteRecycleIfPossible = 0x80

	hrOK               = 0
	hrFalse            = 1
	hrNoInterface      = 0x80004002
	hrAbort            = 0x80004004
	hrChangedMode      = 0x80010106
	hrSharingViolation = 0x80070020
)

// Номера методов в таблицах интерфейсов (первые три — IUnknown).
const (
	vtRelease = 2

	vtAdvise                  = 3
	vtUnadvise                = 4
	vtSetOperationFlags       = 5
	vtDeleteItem              = 18
	vtPerformOperations       = 21
	vtGetAnyOperationsAborted = 22

	sinkMethods      = 19
	sinkPreDeleteIdx = 11
)

// deleteSink — COM-объект IFileOperationProgressSink. Один на программу:
// вызовы trash идут по очереди под trashMu.
type deleteSink struct {
	vtbl *[sinkMethods]uintptr
}

var (
	trashMu  sync.Mutex
	sinkOnce sync.Once
	sink     deleteSink
	// sinkNuke выставляет обработчик, когда оболочка собралась удалить мимо Корзины.
	sinkNuke bool
)

func initSink() {
	// Методам, которые только возвращают «хорошо», хватает одной функции:
	// на 64-битных системах аргументы со стека снимает вызывающий, так что
	// их число значения не имеет. На 32-битной x86 так делать нельзя.
	ok := windows.NewCallback(func(this uintptr) uintptr { return hrOK })
	ref := windows.NewCallback(func(this uintptr) uintptr { return 1 })

	var vtbl [sinkMethods]uintptr
	for i := range vtbl {
		vtbl[i] = ok
	}
	vtbl[0] = windows.NewCallback(sinkQueryInterface)
	vtbl[1] = ref
	vtbl[2] = ref
	vtbl[sinkPreDeleteIdx] = windows.NewCallback(sinkPreDelete)
	sink.vtbl = &vtbl
}

func sinkQueryInterface(this *deleteSink, iid *windows.GUID, out **deleteSink) uintptr {
	if *iid == iidUnknown || *iid == iidProgressSink {
		*out = this
		return hrOK
	}
	*out = nil
	return hrNoInterface
}

func sinkPreDelete(this, flags, item uintptr) uintptr {
	if flags&tsfDeleteRecycleIfPossible == 0 {
		sinkNuke = true
		return hrAbort
	}
	return hrOK
}

func failed(hr uintptr) bool {
	return int32(hr) < 0
}

// method возвращает адрес метода COM-объекта по номеру в таблице.
func method(obj unsafe.Pointer, index int) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

func release(obj unsafe.Pointer) {
	syscall.SyscallN(method(obj, vtRelease), uintptr(obj))
}

func trashUnavailable(path, step string, hr uintptr) error {
	return fmt.Errorf("%w: %s: %s: HRESULT 0x%08X", ErrTrashUnavailable, path, step, uint32(hr))
}

func trash(path string) error {
	trashMu.Lock()
	defer trashMu.Unlock()
	sinkOnce.Do(initSink)

	// COM привязан к потоку ОС.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	var errno syscall.Errno
	switch {
	case err == nil, errors.As(err, &errno) && uint32(errno) == hrFalse:
		defer windows.CoUninitialize()
	case errors.As(err, &errno) && uint32(errno) == hrChangedMode:
		// Поток уже работает с COM в другом режиме — нам подходит и он.
	default:
		return fmt.Errorf("%w: %s: CoInitializeEx: %v", ErrTrashUnavailable, path, err)
	}

	var op unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOperation)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&op)))
	if failed(hr) {
		return trashUnavailable(path, "CoCreateInstance", hr)
	}
	defer release(op)

	hr, _, _ = syscall.SyscallN(method(op, vtSetOperationFlags), uintptr(op),
		fofAllowUndo|fofNoUI|fofxRecycleOnDelete|fofxEarlyFailure)
	if failed(hr) {
		return trashUnavailable(path, "SetOperationFlags", hr)
	}

	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var item unsafe.Pointer
	hr, _, _ = procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(path16)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if failed(hr) {
		// Сюда попадают и пути длиннее 260 символов: оболочка их не понимает.
		return trashUnavailable(path, "SHCreateItemFromParsingName", hr)
	}
	defer release(item)

	var cookie uint32
	hr, _, _ = syscall.SyscallN(method(op, vtAdvise), uintptr(op),
		uintptr(unsafe.Pointer(&sink)), uintptr(unsafe.Pointer(&cookie)))
	if failed(hr) {
		return trashUnavailable(path, "Advise", hr)
	}
	defer func() {
		syscall.SyscallN(method(op, vtUnadvise), uintptr(op), uintptr(cookie))
	}()

	hr, _, _ = syscall.SyscallN(method(op, vtDeleteItem), uintptr(op), uintptr(item), 0)
	if failed(hr) {
		return trashUnavailable(path, "DeleteItem", hr)
	}

	sinkNuke = false
	hr, _, _ = syscall.SyscallN(method(op, vtPerformOperations), uintptr(op))
	var aborted int32
	syscall.SyscallN(method(op, vtGetAnyOperationsAborted), uintptr(op), uintptr(unsafe.Pointer(&aborted)))

	if sinkNuke {
		return i18n.Errorf("%w: %s: для этого диска Корзина отключена или файл в неё не помещается", ErrTrashUnavailable, path)
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if uint32(hr) == hrSharingViolation || heldOpen(path) {
		return fmt.Errorf("%w: %s", ErrBusy, path)
	}
	return trashUnavailable(path, "PerformOperations", hr)
}
