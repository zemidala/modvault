package nexus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zemidala/modvault/i18n"
)

// Адреса, заголовки и поля ответов сверены с официальным клиентом Nexus
// (github.com/Nexus-Mods/node-nexus-api, им пользуется Vortex) версии
// protocolVersion; её же сервер ждёт в заголовке Protocol-Version.
const protocolVersion = "1.7.3"

// DefaultBase — адрес API Nexus Mods.
const DefaultBase = "https://api.nexusmods.com/v1"

// DefaultGraph — адрес нового API Nexus Mods (GraphQL). Личные сообщения
// есть только в нём.
const DefaultGraph = "https://api.nexusmods.com/v2/graphql"

var (
	// ErrNoKey — ключ API не задан.
	ErrNoKey = i18n.NewError("ключ Nexus не задан")
	// ErrUnauthorized — Nexus не принял ключ.
	ErrUnauthorized = i18n.NewError("Nexus не принял ключ")
	// ErrForbidden — действие недоступно этой учётной записи (например,
	// прямая загрузка без Premium) или ссылка с сайта устарела.
	ErrForbidden = i18n.NewError("Nexus отказал")
	// ErrNotFound — такого мода или файла на Nexus нет.
	ErrNotFound = i18n.NewError("на Nexus не найдено")
	// ErrRateLimited — исчерпан лимит запросов.
	ErrRateLimited = i18n.NewError("лимит запросов к Nexus исчерпан")
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
	Graph   string // пусто — DefaultGraph
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
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// do выполняет запрос к API; body, если задано, уходит в теле как JSON.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
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
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
		return i18n.Errorf("Nexus недоступен: %w", err)
	}
	defer resp.Body.Close()
	c.remember(resp.Header)

	answer, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return i18n.Errorf("Nexus недоступен: %w", err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		if err := json.Unmarshal(answer, out); err != nil {
			return i18n.Errorf("непонятный ответ Nexus: %w", err)
		}
		return nil
	}

	var apiErr struct {
		Message string `json:"message"`
	}
	json.Unmarshal(answer, &apiErr)
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
	return detail(i18n.Errorf("Nexus ответил %s", resp.Status))
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
	// CategoryID — номер категории мода в этой игре.
	CategoryID int `json:"category_id"`
	// Endorsements — сколько пользователей одобрили мод; Downloads и
	// UniqueDownloads — сколько раз его скачали всего и сколько разных людей.
	Endorsements    int `json:"endorsement_count"`
	Downloads       int `json:"mod_downloads"`
	UniqueDownloads int `json:"mod_unique_downloads"`
	// ProfileURL — страница того, кто выложил мод; Uploader — он же по номеру.
	ProfileURL string `json:"uploaded_users_profile_url"`
	Uploader   struct {
		ID int `json:"member_id"`
	} `json:"user"`
}

// AuthorPage возвращает адрес профиля автора мода на сайте Nexus или пустую
// строку, если Nexus его не сообщил. Адрес из ответа принимается, только
// если он ведёт на сам Nexus: его потом открывает браузер.
func (m ModInfo) AuthorPage(game string) string {
	if u, err := url.Parse(m.ProfileURL); err == nil && u.Scheme == "https" &&
		(u.Host == "nexusmods.com" || strings.HasSuffix(u.Host, ".nexusmods.com")) {
		return u.String()
	}
	if m.Uploader.ID > 0 {
		return fmt.Sprintf("https://www.nexusmods.com/%s/users/%d", url.PathEscape(game), m.Uploader.ID)
	}
	return ""
}

// graph выполняет запрос к новому API. Ошибка в ответе — ошибка запроса.
func (c *Client) graph(ctx context.Context, query string, vars map[string]any, out any) error {
	u := c.Graph
	if u == "" {
		u = DefaultGraph
	}
	data, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" { // открытые сведения отдаются и без ключа
		req.Header.Set("apikey", c.Key)
	}
	req.Header.Set("User-Agent", "Modvault/"+c.Version)
	req.Header.Set("Application-Name", "Modvault")
	req.Header.Set("Application-Version", c.Version)
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return i18n.Errorf("Nexus недоступен: %w", err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return i18n.Errorf("Nexus недоступен: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return i18n.Errorf("Nexus ответил %s", resp.Status)
	}
	var reply struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(answer, &reply); err != nil {
		return i18n.Errorf("непонятный ответ Nexus: %w", err)
	}
	if len(reply.Errors) > 0 {
		if reply.Errors[0].Extensions.Code == "NOT_FOUND" {
			return fmt.Errorf("%w: %s", ErrNotFound, reply.Errors[0].Message)
		}
		return fmt.Errorf("%w: %s", ErrForbidden, reply.Errors[0].Message)
	}
	if err := json.Unmarshal(reply.Data, out); err != nil {
		return i18n.Errorf("непонятный ответ Nexus: %w", err)
	}
	return nil
}

// SendMessage отправляет личное сообщение пользователю Nexus с номером to
// от имени владельца ключа. Ответ придёт на сайт: прочитать входящие
// через API нельзя.
func (c *Client) SendMessage(ctx context.Context, to int, title, body string) error {
	var out struct {
		CreateMessage struct {
			Success bool `json:"success"`
		} `json:"createMessage"`
	}
	if c.Key == "" {
		return ErrNoKey
	}
	const query = "mutation($to: [Int!]!, $title: String!, $body: String!) { createMessage(to: $to, title: $title, body: $body) { success } }"
	if err := c.graph(ctx, query, map[string]any{"to": []int{to}, "title": title, "body": body}, &out); err != nil {
		return err
	}
	if !out.CreateMessage.Success {
		return i18n.NewError("Nexus не принял сообщение")
	}
	return nil
}

// Одобрение мода пользователем, как его называет Nexus.
const (
	Endorsed  = "Endorsed"
	Abstained = "Abstained"
)

// Endorsement — отметка пользователя у мода.
type Endorsement struct {
	ModID  int    `json:"mod_id"`
	Game   string `json:"domain_name"`
	Status string `json:"status"` // Endorsed, Abstained или Undecided
}

// Endorsements возвращает отметки владельца ключа у модов всех игр.
func (c *Client) Endorsements(ctx context.Context) ([]Endorsement, error) {
	var out []Endorsement
	err := c.get(ctx, "/user/endorsements.json", nil, &out)
	return out, err
}

// Endorse одобряет мод (endorse) или снимает одобрение. version — версия
// мода, которой пользуется владелец ключа. Возвращает новую отметку.
func (c *Client) Endorse(ctx context.Context, game string, modID int, version string, endorse bool) (string, error) {
	action := "abstain"
	if endorse {
		action = "endorse"
	}
	var out struct {
		Status string `json:"status"`
	}
	path := fmt.Sprintf("/games/%s/mods/%d/%s.json", url.PathEscape(game), modID, action)
	err := c.do(ctx, http.MethodPost, path, nil, map[string]string{"Version": version}, &out)
	return out.Status, err
}

// Mod возвращает сведения о моде.
func (c *Client) Mod(ctx context.Context, game string, modID int) (ModInfo, error) {
	var m ModInfo
	err := c.get(ctx, fmt.Sprintf("/games/%s/mods/%d.json", url.PathEscape(game), modID), nil, &m)
	return m, err
}

// Categories возвращает категории модов игры: номер → название.
func (c *Client) Categories(ctx context.Context, game string) (map[int]string, error) {
	var info struct {
		Categories []struct {
			ID   int    `json:"category_id"`
			Name string `json:"name"`
		} `json:"categories"`
	}
	if err := c.get(ctx, fmt.Sprintf("/games/%s.json", url.PathEscape(game)), nil, &info); err != nil {
		return nil, err
	}
	out := make(map[int]string, len(info.Categories))
	for _, cat := range info.Categories {
		out[cat.ID] = cat.Name
	}
	return out, nil
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
		return nil, i18n.NewError("Nexus не дал ссылок на файл")
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
