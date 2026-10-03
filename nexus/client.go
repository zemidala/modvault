package nexus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Адреса, заголовки и поля ответов сверены с официальным клиентом Nexus
// (github.com/Nexus-Mods/node-nexus-api, им пользуется Vortex) версии
// protocolVersion; её же сервер ждёт в заголовке Protocol-Version.
const protocolVersion = "1.7.3"

// DefaultBase — адрес API Nexus Mods.
const DefaultBase = "https://api.nexusmods.com/v1"

var (
	// ErrNoKey — ключ API не задан.
	ErrNoKey = errors.New("ключ Nexus не задан")
	// ErrUnauthorized — Nexus не принял ключ.
	ErrUnauthorized = errors.New("Nexus не принял ключ")
	// ErrForbidden — действие недоступно этой учётной записи (например,
	// прямая загрузка без Premium) или ссылка с сайта устарела.
	ErrForbidden = errors.New("Nexus отказал")
	// ErrNotFound — такого мода или файла на Nexus нет.
	ErrNotFound = errors.New("на Nexus не найдено")
	// ErrRateLimited — исчерпан лимит запросов.
	ErrRateLimited = errors.New("лимит запросов к Nexus исчерпан")
)

// Limits — сколько запросов осталось, по заголовкам последнего ответа.
type Limits struct {
	Known  bool
	Daily  int
	Hourly int
}

// Client — клиент API Nexus Mods. Годится для вызова из разных потоков.
type Client struct {
	Base    string // пусто — DefaultBase
	Key     string
	Version string // версия программы для заголовков запроса
	HTTP    *http.Client

	mu     sync.Mutex
	limits Limits
}

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

// Limits возвращает остаток запросов после последнего ответа.
func (c *Client) Limits() Limits {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limits
}

func (c *Client) remember(h http.Header) {
	daily, derr := strconv.Atoi(h.Get("X-RL-Daily-Remaining"))
	hourly, herr := strconv.Atoi(h.Get("X-RL-Hourly-Remaining"))
	if derr != nil || herr != nil {
		return
	}
	c.mu.Lock()
	c.limits = Limits{Known: true, Daily: daily, Hourly: hourly}
	c.mu.Unlock()
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	if c.Key == "" {
		return ErrNoKey
	}
	u := c.Base
	if u == "" {
		u = DefaultBase
	}
	u += path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", c.Key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Modvault/"+c.Version)
	req.Header.Set("Application-Name", "Modvault")
	req.Header.Set("Application-Version", c.Version)
	req.Header.Set("Protocol-Version", protocolVersion)

	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		// В тексте ошибки сети есть адрес запроса, а в нём может быть ключ
		// ссылки с сайта: наружу идёт только причина.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("Nexus недоступен: %w", err)
	}
	defer resp.Body.Close()
	c.remember(resp.Header)

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("Nexus недоступен: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("непонятный ответ Nexus: %w", err)
		}
		return nil
	}

	var apiErr struct {
		Message string `json:"message"`
	}
	json.Unmarshal(body, &apiErr)
	detail := func(sentinel error) error {
		if apiErr.Message == "" {
			return sentinel
		}
		return fmt.Errorf("%w: %s", sentinel, apiErr.Message)
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return detail(ErrUnauthorized)
	case http.StatusForbidden, http.StatusGone:
		return detail(ErrForbidden)
	case http.StatusNotFound:
		return detail(ErrNotFound)
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return detail(fmt.Errorf("Nexus ответил %s", resp.Status))
}

// User — владелец ключа.
type User struct {
	ID      int    `json:"user_id"`
	Name    string `json:"name"`
	Premium bool   `json:"is_premium"`
}

// Validate проверяет ключ и сообщает, чей он.
func (c *Client) Validate(ctx context.Context) (User, error) {
	var u User
	err := c.get(ctx, "/users/validate.json", nil, &u)
	return u, err
}

// ModInfo — страница мода.
type ModInfo struct {
	ID      int    `json:"mod_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Author — автор, как он назван на странице; UploadedBy — кто выложил.
	Author     string `json:"author"`
	UploadedBy string `json:"uploaded_by"`
}

// Mod возвращает сведения о моде.
func (c *Client) Mod(ctx context.Context, game string, modID int) (ModInfo, error) {
	var m ModInfo
	err := c.get(ctx, fmt.Sprintf("/games/%s/mods/%d.json", url.PathEscape(game), modID), nil, &m)
	return m, err
}

// Разделы файлов на странице мода.
const (
	CategoryMain     = "MAIN"
	CategoryUpdate   = "UPDATE"
	CategoryOptional = "OPTIONAL"
	CategoryOld      = "OLD_VERSION"
	CategoryMisc     = "MISCELLANEOUS"
	CategoryRemoved  = "REMOVED"
	CategoryArchived = "ARCHIVED"
)

// File — файл мода на Nexus.
type File struct {
	ID         int    `json:"file_id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	ModVersion string `json:"mod_version"`
	Category   string `json:"category_name"`
	FileName   string `json:"file_name"`
	// Size — размер в байтах. В описании клиента Nexus этого поля нет (там
	// только размер в килобайтах), так что 0 значит «точный размер неизвестен».
	Size     int64 `json:"size_in_bytes"`
	Uploaded int64 `json:"uploaded_timestamp"`
}

// Optional сообщает, что файл — дополнение к моду, а не сам мод.
func (f File) Optional() bool {
	return f.Category == CategoryOptional || f.Category == CategoryMisc
}

// FileUpdate — «файл New заменил файл Old».
type FileUpdate struct {
	Old int `json:"old_file_id"`
	New int `json:"new_file_id"`
}

// Files — файлы мода и история их замен.
type Files struct {
	Files   []File       `json:"files"`
	Updates []FileUpdate `json:"file_updates"`
}

// Files возвращает файлы мода.
func (c *Client) Files(ctx context.Context, game string, modID int) (Files, error) {
	var f Files
	err := c.get(ctx, fmt.Sprintf("/games/%s/mods/%d/files.json", url.PathEscape(game), modID), nil, &f)
	return f, err
}

// File возвращает сведения об одном файле мода.
func (c *Client) File(ctx context.Context, game string, modID, fileID int) (File, error) {
	var f File
	err := c.get(ctx, fmt.Sprintf("/games/%s/mods/%d/files/%d.json", url.PathEscape(game), modID, fileID), nil, &f)
	return f, err
}

// DownloadLinks возвращает адреса, с которых можно скачать файл. key и
// expires — из ссылки nxm с сайта; без Premium Nexus без них откажет.
func (c *Client) DownloadLinks(ctx context.Context, game string, modID, fileID int, key, expires string) ([]string, error) {
	q := url.Values{}
	if key != "" {
		q.Set("key", key)
		q.Set("expires", expires)
	}
	var mirrors []struct {
		URI string `json:"URI"`
	}
	path := fmt.Sprintf("/games/%s/mods/%d/files/%d/download_link.json", url.PathEscape(game), modID, fileID)
	if err := c.get(ctx, path, q, &mirrors); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range mirrors {
		if m.URI != "" {
			out = append(out, m.URI)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Nexus не дал ссылок на файл")
	}
	return out, nil
}

// Updated — мод, у которого недавно менялись файлы.
type Updated struct {
	ModID            int   `json:"mod_id"`
	LatestFileUpdate int64 `json:"latest_file_update"`
}

// Периоды для Updated.
const (
	PeriodDay   = "1d"
	PeriodWeek  = "1w"
	PeriodMonth = "1m"
)

// Updated возвращает моды игры, менявшиеся за период, одним запросом.
func (c *Client) Updated(ctx context.Context, game, period string) ([]Updated, error) {
	var out []Updated
	err := c.get(ctx, fmt.Sprintf("/games/%s/mods/updated.json", url.PathEscape(game)), url.Values{"period": {period}}, &out)
	return out, err
}

// KnownMD5 сообщает, знает ли Nexus архив с таким MD5 как этот файл мода.
// found — Nexus вообще знает такой архив: свежие файлы он узнаёт не сразу.
func (c *Client) KnownMD5(ctx context.Context, game, md5 string, modID, fileID int) (match, found bool, err error) {
	var hits []struct {
		Mod struct {
			ID int `json:"mod_id"`
		} `json:"mod"`
		File struct {
			ID int `json:"file_id"`
		} `json:"file_details"`
	}
	err = c.get(ctx, fmt.Sprintf("/games/%s/mods/md5_search/%s.json", url.PathEscape(game), url.PathEscape(md5)), nil, &hits)
	if errors.Is(err, ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	for _, h := range hits {
		if h.Mod.ID == modID && h.File.ID == fileID {
			return true, true, nil
		}
	}
	return false, len(hits) > 0, nil
}
