// Package journal — журнал операций: план записывается на диск до первого
// действия, каждый выполненный шаг отмечается. После сбоя по журналу видно,
// что успело произойти, и операцию можно откатить.
//
// План сбрасывается на диск сразу, отметки — раз в SyncEvery: сброс каждой
// отметки стоил бы дороже самого шага. Поэтому после отключения питания
// отметок может оказаться меньше, чем выполнено шагов. Отмеченные шаги
// выполнены наверняка; шаги после последней отметки вызывающий сверяет
// с диском сам.
package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zemidala/modvault/core/fsx"
)

const (
	planFile = "journal.json"
	logFile  = "journal.log"
)

// SyncEvery — как часто отметки сбрасываются на диск.
const SyncEvery = time.Second

// ErrPending — в папке уже лежит незавершённая операция: сначала её нужно
// откатить или закрыть.
var ErrPending = errors.New("есть незавершённая операция")

type record struct {
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

// Journal — открытая операция.
type Journal struct {
	dir    string
	log    *os.File
	done   int
	synced time.Time // когда отметки в последний раз сброшены на диск
}

// Begin записывает план операции data и открывает журнал. Пока журнал
// не закрыт, в той же папке новую операцию начать нельзя.
func Begin(dir, id string, data any) (*Journal, error) {
	if _, err := os.Stat(filepath.Join(dir, planFile)); err == nil {
		return nil, ErrPending
	}
	// Отметки без плана остаются, если прошлый Clear оборвался.
	if err := os.Remove(filepath.Join(dir, logFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	plan, err := json.Marshal(record{ID: id, Data: raw})
	if err != nil {
		return nil, err
	}
	if err := fsx.WriteFile(filepath.Join(dir, planFile), plan); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(dir, logFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		os.Remove(filepath.Join(dir, planFile))
		return nil, err
	}
	return &Journal{dir: dir, log: log, synced: time.Now()}, nil
}

// Done отмечает, что очередной шаг выполнен. Отметка записывается сразу
// (обрыв программы её не теряет), а на диск сбрасывается раз в SyncEvery.
func (j *Journal) Done() error {
	if _, err := fmt.Fprintf(j.log, "%d\n", j.done); err != nil {
		return err
	}
	if time.Since(j.synced) >= SyncEvery {
		if err := j.log.Sync(); err != nil {
			return err
		}
		j.synced = time.Now()
	}
	j.done++
	return nil
}

// Close закрывает файл отметок, не удаляя журнал.
func (j *Journal) Close() error {
	return j.log.Close()
}

// Finish закрывает журнал и удаляет его: операция завершена или откачена.
func (j *Journal) Finish() error {
	j.log.Close()
	return Clear(j.dir)
}

// Pending — операция, которая не успела завершиться.
type Pending struct {
	ID   string
	Data json.RawMessage
	// Done — сколько шагов отмечено выполненными. Шаг с номером Done мог
	// начаться, но не успеть получить отметку; после отключения питания без
	// отметки могли остаться и несколько шагов за ним.
	Done int
}

// Load возвращает незавершённую операцию или nil, если её нет.
func Load(dir string) (*Pending, error) {
	data, err := os.ReadFile(filepath.Join(dir, planFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("журнал %s повреждён: %w", dir, err)
	}
	p := &Pending{ID: r.ID, Data: r.Data}

	log, err := os.ReadFile(filepath.Join(dir, logFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	// Отметки идут подряд с нуля; оборванная последняя строка не считается.
	sc := bufio.NewScanner(bytes.NewReader(log))
	for sc.Scan() {
		n, err := strconv.Atoi(sc.Text())
		if err != nil || n != p.Done {
			break
		}
		p.Done++
	}
	return p, nil
}

// Clear удаляет журнал. План удаляется первым: без него отметки ничего
// не значат, а следующий Begin их уберёт.
func Clear(dir string) error {
	if err := os.Remove(filepath.Join(dir, planFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Join(dir, logFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
