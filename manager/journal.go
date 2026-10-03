package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/i18n"
)

// Журнал действий: что программа сделала с модами и игрой и что не
// удалось. Запись — строка JSON в файле journal.jsonl хранилища; файл
// только дописывается, поэтому обрыв записи портит не больше одной строки.

const (
	journalFile = "journal.jsonl"
	// journalKeep — сколько последних записей остаётся, когда файл разросся.
	journalKeep = 1000
	journalMax  = 512 << 10
)

// Виды записей журнала.
const (
	EventDeploy  = "deploy"  // развёртывание в игру
	EventInstall = "install" // мод добавлен или обновлён
	EventRemove  = "remove"  // мод удалён
	EventSet     = "set"     // действия с наборами
	EventNexus   = "nexus"   // проверка обновлений, вход
	EventVortex  = "vortex"  // перенять и вернуть
	EventError   = "error"   // действие не удалось
)

// Event — запись журнала действий.
type Event struct {
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

func (a *Manager) journalPath() string { return filepath.Join(a.home, journalFile) }

// note дописывает запись в журнал. Журнал — подспорье, а не учёт: если
// записать не вышло, действие всё равно считается выполненным.
func (a *Manager) note(kind, text string) {
	if a.openErr != nil || text == "" {
		return
	}
	line, err := json.Marshal(Event{Time: time.Now().UTC().Truncate(time.Second), Kind: kind, Text: text})
	if err != nil {
		return
	}
	path := a.journalPath()
	if info, err := os.Stat(path); err == nil && info.Size() > journalMax {
		a.trimJournal()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// noteError записывает, что действие не удалось.
func (a *Manager) noteError(what string, err error) {
	if err != nil {
		a.note(EventError, what+": "+err.Error())
	}
}

// trimJournal оставляет в журнале последние записи.
func (a *Manager) trimJournal() {
	events, err := a.readJournal()
	if err != nil || len(events) <= journalKeep {
		return
	}
	var buf bytes.Buffer
	for _, e := range events[len(events)-journalKeep:] {
		if line, err := json.Marshal(e); err == nil {
			buf.Write(line)
			buf.WriteByte('\n')
		}
	}
	fsx.WriteFile(a.journalPath(), buf.Bytes())
}

// readJournal читает журнал от старых записей к новым; испорченные строки пропускает.
func (a *Manager) readJournal() ([]Event, error) {
	data, err := os.ReadFile(a.journalPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Event
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Text != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

// Journal возвращает последние записи журнала, от новых к старым.
func (a *Manager) Journal(limit int) ([]Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return nil, a.openErr
	}
	events, err := a.readJournal()
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(events))
	for i := len(events) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, events[i])
	}
	return out, nil
}

// Загрузки этого запуска программы: что качается и чем кончилось.

// Состояния загрузки.
const (
	DownloadActive = "active"
	DownloadDone   = "done"
	DownloadFailed = "failed"
)

// maxDownloads — сколько загрузок помнить за запуск.
const maxDownloads = 50

// Download — загрузка файла мода с Nexus.
type Download struct {
	Seq      int       `json:"seq"` // порядковый номер загрузки в этом запуске
	ModID    int       `json:"modId"`
	FileID   int       `json:"fileId"`
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	Done     int64     `json:"done"`
	Total    int64     `json:"total"` // 0 — размер неизвестен
	State    string    `json:"state"`
	Message  string    `json:"message"` // итог или причина неудачи
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	// Speed — скорость загрузки, байт в секунду; 0 — ещё не измерена.
	Speed int64 `json:"speed"`

	// Замер скорости: сколько было скачано и когда.
	sampleAt   time.Time
	sampleDone int64
}

// Downloads возвращает загрузки этого запуска, от новых к старым.
func (a *Manager) Downloads() []Download {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Download, 0, len(a.downloads))
	for i := len(a.downloads) - 1; i >= 0; i-- {
		out = append(out, *a.downloads[i])
	}
	return out
}

func (a *Manager) startDownload(modID, fileID int) *Download {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.downloadSeq++
	d := &Download{
		Seq: a.downloadSeq, ModID: modID, FileID: fileID,
		Name: i18n.T("Мод ") + itoa(modID), State: DownloadActive, Started: time.Now().UTC(),
	}
	a.downloads = append(a.downloads, d)
	if len(a.downloads) > maxDownloads {
		a.downloads = a.downloads[len(a.downloads)-maxDownloads:]
	}
	return d
}

// describeDownload записывает, что именно качается, когда это стало известно.
func (a *Manager) describeDownload(d *Download, name, version string, total int64) {
	a.mu.Lock()
	d.Name, d.Version, d.Total = name, version, total
	a.mu.Unlock()
}

func (a *Manager) progressDownload(d *Download, done, total int64) {
	a.mu.Lock()
	d.Done, d.Total = done, total
	// Скорость — за последнюю секунду с небольшим: так она не скачет от
	// куска к куску и не отстаёт от настоящей.
	switch now := time.Now(); {
	case d.sampleAt.IsZero():
		d.sampleAt, d.sampleDone = now, done
	case now.Sub(d.sampleAt) >= time.Second:
		d.Speed = int64(float64(done-d.sampleDone) / now.Sub(d.sampleAt).Seconds())
		d.sampleAt, d.sampleDone = now, done
	}
	a.mu.Unlock()
}

// finishDownload отмечает итог загрузки и записывает его в журнал.
func (a *Manager) finishDownload(d *Download, message string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d.Finished = time.Now().UTC()
	d.Speed = 0
	if err != nil {
		d.State, d.Message = DownloadFailed, err.Error()
		a.noteError(i18n.Sprintf("Загрузка «%s»", d.Name), err)
		return
	}
	d.State, d.Message = DownloadDone, message
	if d.Total > 0 {
		d.Done = d.Total
	}
	a.note(EventInstall, message)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
