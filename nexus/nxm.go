package nexus

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Link — ссылка nxm://, которую сайт отдаёт по кнопке «Mod Manager Download».
type Link struct {
	Game    string // имя игры в адресах Nexus
	ModID   int
	FileID  int
	Key     string // разовый ключ загрузки; пуст у ссылок для Premium
	Expires string
}

// IsLink сообщает, что строка похожа на ссылку nxm.
func IsLink(s string) bool {
	return len(s) >= 6 && strings.EqualFold(s[:6], "nxm://")
}

// ParseLink разбирает ссылку вида
// nxm://игра/mods/123/files/456?key=…&expires=…&user_id=….
func ParseLink(raw string) (Link, error) {
	bad := func(why string) (Link, error) {
		return Link{}, fmt.Errorf("ссылка nxm не разобрана: %s", why)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "nxm") {
		return bad("это не ссылка nxm://")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 1 && strings.EqualFold(parts[0], "collections") {
		return bad("коллекции не поддерживаются")
	}
	if u.Host == "" || len(parts) != 4 || !strings.EqualFold(parts[0], "mods") || !strings.EqualFold(parts[2], "files") {
		return bad("ожидается nxm://игра/mods/номер/files/номер")
	}
	modID, err1 := strconv.Atoi(parts[1])
	fileID, err2 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || modID <= 0 || fileID <= 0 {
		return bad("номера мода и файла должны быть числами")
	}
	q := u.Query()
	return Link{
		Game: strings.ToLower(u.Host), ModID: modID, FileID: fileID,
		Key: q.Get("key"), Expires: q.Get("expires"),
	}, nil
}

// ModPage возвращает адрес страницы мода на сайте.
func ModPage(game string, modID int) string {
	return fmt.Sprintf("https://www.nexusmods.com/%s/mods/%d", url.PathEscape(game), modID)
}

// FilesPage возвращает адрес списка файлов мода на сайте.
func FilesPage(game string, modID int) string {
	return ModPage(game, modID) + "?tab=files"
}
