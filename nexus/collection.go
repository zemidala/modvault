package nexus

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Collection — коллекция модов на Nexus в одной из своих редакций.
type Collection struct {
	Name     string
	Slug     string
	Author   string
	Game     string // имя игры в адресах Nexus
	Revision int
	Mods     []CollectionMod
	// External — то, что автор коллекции просит взять не с Nexus.
	External []CollectionResource
}

// CollectionMod — файл мода, входящий в коллекцию.
type CollectionMod struct {
	ModID    int
	FileID   int
	Name     string // название мода
	Version  string
	Optional bool // автор коллекции считает мод необязательным
}

// CollectionResource — ресурс вне Nexus.
type CollectionResource struct {
	Name     string
	URL      string
	Optional bool
}

// CollectionLink — куда указывает ссылка на коллекцию.
type CollectionLink struct {
	Game     string // пусто — в ссылке игры нет
	Slug     string
	Revision int // 0 — последняя опубликованная
}

// ParseCollection разбирает ссылку на коллекцию: адрес её страницы на
// сайте (…/collections/код, …/collections/код/revisions/N), ссылку
// nxm://игра/collections/код/revisions/N или сам код коллекции.
func ParseCollection(raw string) (CollectionLink, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CollectionLink{}, false
	}
	if !strings.Contains(raw, "/") {
		return CollectionLink{Slug: raw}, isSlug(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return CollectionLink{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if strings.EqualFold(u.Scheme, "nxm") {
		parts = append([]string{u.Host}, parts...)
	}
	var link CollectionLink
	for i, part := range parts {
		switch {
		case strings.EqualFold(part, "collections") && i+1 < len(parts):
			link.Slug = parts[i+1]
			// Перед «collections» стоит имя игры; «games» — часть адреса сайта.
			if i > 0 && !strings.EqualFold(parts[i-1], "games") {
				link.Game = strings.ToLower(parts[i-1])
			}
		case strings.EqualFold(part, "revisions") && i+1 < len(parts):
			link.Revision, _ = strconv.Atoi(parts[i+1])
		}
	}
	return link, isSlug(link.Slug)
}

// isSlug — код коллекции: латинские буквы и цифры.
func isSlug(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// IsCollectionLink сообщает, что ссылка nxm ведёт на коллекцию.
func IsCollectionLink(raw string) bool {
	if !IsLink(raw) {
		return false
	}
	_, ok := ParseCollection(raw)
	return ok
}

// CollectionPage возвращает адрес страницы коллекции на сайте.
func CollectionPage(game, slug string) string {
	return fmt.Sprintf("https://www.nexusmods.com/games/%s/collections/%s", url.PathEscape(game), url.PathEscape(slug))
}

const collectionQuery = `query($slug: String!, $domain: String, $revision: Int) {
  collectionRevision(slug: $slug, domainName: $domain, revision: $revision, viewAdultContent: true) {
    revisionNumber
    collection { name slug user { name } game { domainName } }
    modFiles { fileId optional version file { fileId modId name version mod { name } } }
    externalResources { name resourceUrl optional }
  }
}`

// Collection возвращает состав коллекции; revision 0 — последняя
// опубликованная редакция. Ключ для этого не нужен.
func (c *Client) Collection(ctx context.Context, game, slug string, revision int) (Collection, error) {
	var out struct {
		Rev struct {
			Number     int `json:"revisionNumber"`
			Collection struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
				User struct {
					Name string `json:"name"`
				} `json:"user"`
				Game struct {
					Domain string `json:"domainName"`
				} `json:"game"`
			} `json:"collection"`
			Files []struct {
				FileID   int    `json:"fileId"`
				Optional bool   `json:"optional"`
				Version  string `json:"version"`
				File     *struct {
					ModID   int    `json:"modId"`
					Name    string `json:"name"`
					Version string `json:"version"`
					Mod     struct {
						Name string `json:"name"`
					} `json:"mod"`
				} `json:"file"`
			} `json:"modFiles"`
			External []struct {
				Name     string `json:"name"`
				URL      string `json:"resourceUrl"`
				Optional bool   `json:"optional"`
			} `json:"externalResources"`
		} `json:"collectionRevision"`
	}
	vars := map[string]any{"slug": slug, "domain": game}
	if revision > 0 {
		vars["revision"] = revision
	}
	if err := c.graph(ctx, collectionQuery, vars, &out); err != nil {
		return Collection{}, err
	}
	r := out.Rev
	col := Collection{
		Name: r.Collection.Name, Slug: r.Collection.Slug, Author: r.Collection.User.Name,
		Game: r.Collection.Game.Domain, Revision: r.Number,
	}
	for _, f := range r.Files {
		if f.File == nil {
			continue // файл убран с Nexus: взять его неоткуда
		}
		m := CollectionMod{ModID: f.File.ModID, FileID: f.FileID, Optional: f.Optional}
		m.Name = f.File.Mod.Name
		if m.Name == "" {
			m.Name = f.File.Name
		}
		m.Version = f.Version
		if m.Version == "" {
			m.Version = f.File.Version
		}
		col.Mods = append(col.Mods, m)
	}
	for _, e := range r.External {
		col.External = append(col.External, CollectionResource{Name: e.Name, URL: e.URL, Optional: e.Optional})
	}
	return col, nil
}
