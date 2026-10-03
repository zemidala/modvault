package nexus

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Сколько неудач подряд (без единого полученного байта) терпит загрузка,
// сколько ждёт между попытками и сколько ждёт молчащий сервер.
var (
	maxFailures  = 5
	retryDelay   = 2 * time.Second
	stallTimeout = 60 * time.Second
)

// Загрузки идут без общего срока: большой файл качается долго. От
// зависшего соединения защищает stallTimeout.
var downloadHTTP = &http.Client{}

// errFatal — повторять загрузку бессмысленно.
type errFatal struct{ error }

// Download качает файл в part, продолжая с того места, где прошлая попытка
// оборвалась. urls — зеркала одного файла. size — ожидаемый размер, 0 —
// неизвестен. Готовый файл остаётся в part: переименовать его должен
// вызывающий, когда убедится, что файл тот.
func Download(ctx context.Context, urls []string, part string, size int64, progress func(done, total int64)) error {
	if len(urls) == 0 {
		return errors.New("Nexus не дал ссылок на файл")
	}
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return err
	}
	if progress == nil {
		progress = func(int64, int64) {}
	}
	var last error
	for attempt, failures := 0, 0; failures < maxFailures; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
			}
		}
		written, err := fetch(ctx, urls[attempt%len(urls)], part, size, progress)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var fatal errFatal
		if errors.As(err, &fatal) {
			return fatal.error
		}
		last = err
		if written > 0 {
			failures = 0 // дело движется: это обрыв, а не тупик
		} else {
			failures++
		}
	}
	return fmt.Errorf("загрузка не удалась: %w", last)
}

// fetch делает одну попытку и возвращает, сколько байт она добавила.
func fetch(ctx context.Context, rawURL, part string, size int64, progress func(done, total int64)) (int64, error) {
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
	}
	if size > 0 && offset > size {
		if err := os.Remove(part); err != nil {
			return 0, errFatal{err}
		}
		offset = 0
	}
	if size > 0 && offset == size {
		return 0, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, errFatal{errors.New("Nexus дал негодную ссылку на файл")}
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	watchdog := time.AfterFunc(stallTimeout, cancel)
	defer watchdog.Stop()
	resp, err := downloadHTTP.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err // без адреса: в нём разовый ключ
		}
		return 0, err
	}
	defer resp.Body.Close()

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0 // сервер отдаёт файл с начала
	case http.StatusPartialContent:
		flags = os.O_WRONLY | os.O_APPEND
	case http.StatusRequestedRangeNotSatisfiable:
		// Начатый файл не подходит к тому, что на сервере: начнём заново.
		if err := os.Remove(part); err != nil {
			return 0, errFatal{err}
		}
		return 0, errors.New("сервер не принял докачку")
	case http.StatusForbidden, http.StatusGone:
		return 0, errFatal{errors.New("ссылка на файл устарела: нажмите кнопку загрузки на сайте ещё раз")}
	default:
		return 0, fmt.Errorf("сервер ответил %s", resp.Status)
	}
	total := size
	if total == 0 && resp.ContentLength > 0 {
		total = offset + resp.ContentLength
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return 0, errFatal{err}
	}
	defer f.Close()

	var written int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(stallTimeout)
			if size > 0 && offset+written+int64(n) > size {
				return written, errFatal{fmt.Errorf("сервер отдаёт больше, чем %d байт, заявленных Nexus", size)}
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return written, errFatal{werr}
			}
			written += int64(n)
			progress(offset+written, total)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return written, rerr
		}
	}
	if err := f.Sync(); err != nil {
		return written, errFatal{err}
	}
	if size > 0 && offset+written != size {
		return written, fmt.Errorf("получено %d байт из %d", offset+written, size)
	}
	return written, nil
}

// MD5File считает MD5 файла: по нему Nexus узнаёт свои архивы.
func MD5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
