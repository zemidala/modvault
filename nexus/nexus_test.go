package nexus

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLink(t *testing.T) {
	l, err := ParseLink("nxm://Warhammer40kDarktide/mods/139/files/4321?key=abc&expires=1700000000&user_id=7")
	if err != nil {
		t.Fatal(err)
	}
	want := Link{Game: "warhammer40kdarktide", ModID: 139, FileID: 4321, Key: "abc", Expires: "1700000000"}
	if l != want {
		t.Errorf("ссылка = %+v; ждали %+v", l, want)
	}
	if l, err := ParseLink(" NXM://skyrim/mods/1/files/2 "); err != nil || l.Key != "" || l.FileID != 2 {
		t.Errorf("ссылка без ключа: %+v, %v", l, err)
	}
	for _, bad := range []string{
		"https://www.nexusmods.com/x/mods/1",
		"nxm://game/collections/abc/revisions/3",
		"nxm://game/mods/x/files/2",
		"nxm://game/mods/1",
		"nxm:///mods/1/files/2",
		"nxm://game/mods/0/files/2",
	} {
		if _, err := ParseLink(bad); err == nil {
			t.Errorf("%q разобрана без ошибки", bad)
		}
	}
	if !IsLink("NXM://x") || IsLink("http://x") || IsLink("nxm") {
		t.Error("IsLink")
	}
}

func TestNewer(t *testing.T) {
	for _, tt := range []struct {
		installed, latest string
		want              bool
	}{
		{"1.4.0", "1.5.0", true},
		{"1.5.0", "1.4.0", false},
		{"1.4", "1.4.0", false},
		{"23.4.05", "23.4.5", false},
		{"v1.2", "1.2", false},
		{"1.9", "1.10", true},
		{"1.0", "1.0b", true},
		{"", "1.0", true},
		{"1.0", "", false},
		{"2026-07-06", "2026-09-01", true},
	} {
		if got := Newer(tt.installed, tt.latest); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v", tt.installed, tt.latest, got)
		}
	}
}

func TestLatest(t *testing.T) {
	files := Files{
		Files: []File{
			{ID: 1, Version: "1.0", Category: CategoryOld, Uploaded: 10},
			{ID: 2, Version: "1.1", Category: CategoryOld, Uploaded: 20},
			{ID: 3, Version: "1.2", Category: CategoryMain, Uploaded: 30},
			{ID: 7, Version: "0.5", Category: CategoryOptional, Uploaded: 40},
			{ID: 8, Version: "0.6", Category: CategoryOptional, Uploaded: 50},
		},
		Updates: []FileUpdate{{Old: 1, New: 2}, {Old: 2, New: 3}, {Old: 7, New: 8}},
	}
	if f, ok := files.Latest(1); !ok || f.ID != 3 {
		t.Errorf("по истории от 1: %+v, %v", f, ok)
	}
	if f, ok := files.Latest(7); !ok || f.ID != 8 {
		t.Errorf("дополнение обновляется дополнением: %+v, %v", f, ok)
	}
	if f, ok := files.Latest(3); !ok || f.ID != 3 {
		t.Errorf("уже последний: %+v, %v", f, ok)
	}
	if f, ok := files.Latest(0); !ok || f.ID != 3 {
		t.Errorf("файл неизвестен — берётся основной: %+v, %v", f, ok)
	}
	if f, ok := files.Latest(99); !ok || f.ID != 3 {
		t.Errorf("файла нет на странице — берётся основной: %+v, %v", f, ok)
	}
	loop := Files{Files: []File{{ID: 1, Category: CategoryMain}}, Updates: []FileUpdate{{Old: 1, New: 2}, {Old: 2, New: 1}}}
	if _, ok := loop.Latest(1); !ok {
		t.Error("история с петлёй")
	}
	if _, ok := (Files{}).Latest(0); ok {
		t.Error("у пустой страницы нашёлся файл")
	}
}

func TestClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Please provide a valid API Key"}`))
			return
		}
		if r.Header.Get("Application-Name") != "Modvault" || r.Header.Get("Application-Version") != "1.2.3" {
			t.Errorf("заголовки программы: %v", r.Header)
		}
		w.Header().Set("X-RL-Daily-Remaining", "2400")
		w.Header().Set("X-RL-Hourly-Remaining", "99")
		switch r.URL.Path {
		case "/users/validate.json":
			w.Write([]byte(`{"user_id":7,"name":"zd","is_premium":true,"key":"good"}`))
		case "/games/g/mods/5/files/9.json":
			w.Write([]byte(`{"file_id":9,"name":"Main","version":"1.5.0","category_name":"MAIN","file_name":"M-5-1-5-0-1.zip","size_in_bytes":null,"uploaded_timestamp":123}`))
		case "/games/g/mods/5/files/9/download_link.json":
			if r.URL.Query().Get("key") != "k" || r.URL.Query().Get("expires") != "e" {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"message":"premium only"}`))
				return
			}
			w.Write([]byte(`[{"name":"CDN","short_name":"cdn","URI":"https://cdn/a.zip"}]`))
		case "/games/g/mods/updated.json":
			if r.URL.Query().Get("period") != "1m" {
				t.Errorf("период: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`[{"mod_id":5,"latest_file_update":100,"latest_mod_activity":200}]`))
		case "/games/g/mods/md5_search/abc.json":
			w.Write([]byte(`[{"mod":{"mod_id":5},"file_details":{"file_id":9}}]`))
		case "/games/g/mods/1.json":
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	bad := &Client{Base: srv.URL, Key: "bad", Version: "1.2.3"}
	if _, err := bad.Validate(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("плохой ключ: %v", err)
	}
	if _, err := (&Client{Base: srv.URL}).Validate(ctx); !errors.Is(err, ErrNoKey) {
		t.Errorf("без ключа: %v", err)
	}

	c := &Client{Base: srv.URL, Key: "good", Version: "1.2.3"}
	u, err := c.Validate(ctx)
	if err != nil || u.Name != "zd" || !u.Premium {
		t.Fatalf("Validate: %+v, %v", u, err)
	}
	if l := c.Limits(); !l.Known || l.Daily != 2400 || l.Hourly != 99 {
		t.Errorf("лимиты: %+v", l)
	}
	f, err := c.File(ctx, "g", 5, 9)
	if err != nil || f.ID != 9 || f.Version != "1.5.0" || f.Size != 0 || f.Optional() {
		t.Errorf("File: %+v, %v", f, err)
	}
	links, err := c.DownloadLinks(ctx, "g", 5, 9, "k", "e")
	if err != nil || len(links) != 1 || links[0] != "https://cdn/a.zip" {
		t.Errorf("DownloadLinks: %v, %v", links, err)
	}
	if _, err := c.DownloadLinks(ctx, "g", 5, 9, "", ""); !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "premium only") {
		t.Errorf("без ключа ссылки: %v", err)
	}
	up, err := c.Updated(ctx, "g", PeriodMonth)
	if err != nil || len(up) != 1 || up[0].ModID != 5 || up[0].LatestFileUpdate != 100 {
		t.Errorf("Updated: %v, %v", up, err)
	}
	if match, found, err := c.KnownMD5(ctx, "g", "abc", 5, 9); err != nil || !match || !found {
		t.Errorf("KnownMD5: %v %v %v", match, found, err)
	}
	if match, found, err := c.KnownMD5(ctx, "g", "abc", 5, 10); err != nil || match || !found {
		t.Errorf("KnownMD5 чужого файла: %v %v %v", match, found, err)
	}
	if match, found, err := c.KnownMD5(ctx, "g", "nope", 5, 9); err != nil || match || found {
		t.Errorf("KnownMD5 неизвестного: %v %v %v", match, found, err)
	}
	if _, err := c.Mod(ctx, "g", 1); !errors.Is(err, ErrRateLimited) {
		t.Errorf("лимит: %v", err)
	}
	if _, err := c.Mod(ctx, "g", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("нет мода: %v", err)
	}
}

func TestClientHidesAddress(t *testing.T) {
	c := &Client{Base: "http://127.0.0.1:1", Key: "k"}
	_, err := c.DownloadLinks(context.Background(), "g", 1, 2, "secret-key", "1")
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Errorf("ошибка сети: %v", err)
	}
}

// fastRetries убирает ожидание между попытками.
func fastRetries(t *testing.T) {
	old := retryDelay
	retryDelay = time.Millisecond
	t.Cleanup(func() { retryDelay = old })
}

func TestDownloadResumes(t *testing.T) {
	fastRetries(t)
	content := bytes.Repeat([]byte("0123456789abcdef"), 40000) // 640 КБ
	var requests, ranged atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if r.Header.Get("Range") != "" {
			ranged.Add(1)
		}
		if n <= 2 {
			// Обрыв посреди ответа: часть отдана, соединение разорвано.
			var start int
			if n == 2 {
				start = 100000
				w.Header().Set("Content-Range", "bytes 100000-639999/640000")
				w.WriteHeader(http.StatusPartialContent)
			}
			w.Write(content[start : start+100000])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, "a.zip", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	part := filepath.Join(t.TempDir(), "dl", "a.zip.part")
	var last, total int64
	err := Download(context.Background(), []string{srv.URL}, part, int64(len(content)), func(done, all int64) {
		if done < last {
			t.Errorf("прогресс пошёл назад: %d после %d", done, last)
		}
		last, total = done, all
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(part)
	if !bytes.Equal(got, content) {
		t.Fatalf("файл не совпал: %d байт из %d", len(got), len(content))
	}
	if requests.Load() != 3 || ranged.Load() != 2 {
		t.Errorf("запросов %d, из них докачек %d", requests.Load(), ranged.Load())
	}
	if last != int64(len(content)) || total != int64(len(content)) {
		t.Errorf("прогресс: %d из %d", last, total)
	}

	// Повторный вызов над готовым файлом в сеть не идёт.
	if err := Download(context.Background(), []string{srv.URL}, part, int64(len(content)), nil); err != nil || requests.Load() != 3 {
		t.Errorf("повтор: %v, запросов %d", err, requests.Load())
	}
	sum, err := MD5File(part)
	if err != nil || len(sum) != 32 {
		t.Errorf("MD5File: %q, %v", sum, err)
	}
}

func TestDownloadFailures(t *testing.T) {
	fastRetries(t)
	var requests atomic.Int32
	status := http.StatusInternalServerError
	body := []byte("0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	ctx := context.Background()
	part := filepath.Join(t.TempDir(), "a.part")

	if err := Download(ctx, []string{srv.URL}, part, 10, nil); err == nil || int(requests.Load()) != maxFailures {
		t.Errorf("сервер не отвечает: %v после %d запросов", err, requests.Load())
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Error("после неудачи остался файл")
	}

	requests.Store(0)
	status = http.StatusForbidden
	if err := Download(ctx, []string{srv.URL}, part, 10, nil); err == nil || requests.Load() != 1 || !strings.Contains(err.Error(), "устарела") {
		t.Errorf("устаревшая ссылка: %v после %d запросов", err, requests.Load())
	}

	// Сервер отдаёт больше заявленного: это не тот файл, повторять незачем.
	requests.Store(0)
	status = http.StatusOK
	if err := Download(ctx, []string{srv.URL}, part, 4, nil); err == nil || requests.Load() != 1 {
		t.Errorf("лишние байты: %v после %d запросов", err, requests.Load())
	}

	// Размер неизвестен: принимается то, что отдал сервер.
	os.Remove(part)
	if err := Download(ctx, []string{srv.URL}, part, 0, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(part); !bytes.Equal(got, body) {
		t.Errorf("файл: %q", got)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := Download(cancelled, []string{srv.URL}, filepath.Join(t.TempDir(), "b.part"), 10, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("отмена: %v", err)
	}
	if err := Download(ctx, nil, part, 10, nil); err == nil {
		t.Error("без ссылок ошибки нет")
	}
}
