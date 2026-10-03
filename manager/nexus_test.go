package manager

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zemidala/modvault/nexus"
)

const testDomain = "warhammer40kdarktide"

type fakeFile struct {
	ID       int    `json:"file_id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Category string `json:"category_name"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size_in_bytes"`
	Uploaded int64  `json:"uploaded_timestamp"`
	content  []byte
}

type fakeMod struct {
	name    string
	files   []*fakeFile
	updates [][2]int // старый файл → новый
}

// fakeNexus — поддельный Nexus: API и сервер файлов.
type fakeNexus struct {
	srv *httptest.Server

	mu       sync.Mutex
	premium  bool
	mods     map[int]*fakeMod
	changed  map[int]int64 // мод → время последней смены файлов
	requests []string      // пути запросов к API
	cut      map[int]int   // файл → сколько раз оборвать отдачу посередине
	broken   map[int]bool  // файл не отдаётся вовсе
	foreign  map[int]bool  // Nexus знает этот архив как другой файл
}

func newFakeNexus(t *testing.T) *fakeNexus {
	f := &fakeNexus{mods: map[int]*fakeMod{}, changed: map[int]int64{}, cut: map[int]int{}, broken: map[int]bool{}, foreign: map[int]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	old := nexus.RetryDelay
	nexus.RetryDelay = time.Millisecond
	t.Cleanup(func() { nexus.RetryDelay = old })
	return f
}

// add кладёт на поддельный Nexus файл мода с архивом из files.
func (f *fakeNexus) add(t *testing.T, modID int, modName string, file fakeFile, files map[string]string) {
	t.Helper()
	data, err := os.ReadFile(writeZip(t, "x.zip", files))
	if err != nil {
		t.Fatal(err)
	}
	file.content, file.Size = data, int64(len(data))
	if file.FileName == "" {
		file.FileName = fmt.Sprintf("%s-%d-%d.zip", modName, modID, file.ID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.mods[modID]
	if !ok {
		m = &fakeMod{name: modName}
		f.mods[modID] = m
	}
	m.files = append(m.files, &file)
}

func (f *fakeNexus) file(id int) *fakeFile {
	for _, m := range f.mods {
		for _, file := range m.files {
			if file.ID == id {
				return file
			}
		}
	}
	return nil
}

func (f *fakeNexus) apiRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeNexus) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path

	if id, ok := strings.CutPrefix(path, "/cdn/"); ok {
		n, _ := strconv.Atoi(id)
		file := f.file(n)
		switch {
		case file == nil || f.broken[n]:
			w.WriteHeader(http.StatusInternalServerError)
		case f.cut[n] > 0 && r.Header.Get("Range") == "":
			f.cut[n]--
			w.Write(file.content[:len(file.content)/2])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		default:
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(file.content))
		}
		return
	}

	f.requests = append(f.requests, path)
	if r.Header.Get("apikey") != "good" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("X-RL-Daily-Remaining", "2000")
	w.Header().Set("X-RL-Hourly-Remaining", "90")
	reply := func(v any) { json.NewEncoder(w).Encode(v) }

	if path == "/users/validate.json" {
		reply(map[string]any{"user_id": 1, "name": "zd", "is_premium": f.premium})
		return
	}
	if path == "/games/"+testDomain+".json" {
		reply(map[string]any{"categories": []map[string]any{{"category_id": 7, "name": "User Interface", "parent_category": false}}})
		return
	}
	rest, ok := strings.CutPrefix(path, "/games/"+testDomain+"/mods/")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if rest == "updated.json" {
		out := []map[string]any{}
		for id, at := range f.changed {
			out = append(out, map[string]any{"mod_id": id, "latest_file_update": at})
		}
		reply(out)
		return
	}
	if sum, ok := strings.CutPrefix(rest, "md5_search/"); ok {
		sum = strings.TrimSuffix(sum, ".json")
		for modID, m := range f.mods {
			for _, file := range m.files {
				if got := md5.Sum(file.content); hex.EncodeToString(got[:]) == sum {
					id := file.ID
					if f.foreign[id] {
						id += 1000
					}
					reply([]map[string]any{{"mod": map[string]any{"mod_id": modID}, "file_details": map[string]any{"file_id": id}}})
					return
				}
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	parts := strings.Split(strings.TrimSuffix(rest, ".json"), "/")
	modID, _ := strconv.Atoi(parts[0])
	m := f.mods[modID]
	if m == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch {
	case len(parts) == 1:
		reply(map[string]any{"mod_id": modID, "name": m.name, "version": "0", "author": "Автор " + m.name, "uploaded_by": "uploader",
			"uploaded_users_profile_url": "https://www.nexusmods.com/users/" + strconv.Itoa(modID*10),
			"endorsement_count":          modID * 100, "mod_downloads": modID * 5000, "category_id": 7, "mod_unique_downloads": modID * 3000})
	case len(parts) == 2 && parts[1] == "files":
		updates := []map[string]int{}
		for _, u := range m.updates {
			updates = append(updates, map[string]int{"old_file_id": u[0], "new_file_id": u[1]})
		}
		reply(map[string]any{"files": m.files, "file_updates": updates})
	case len(parts) == 4 && parts[3] == "download_link":
		if !f.premium && (r.URL.Query().Get("key") != "k" || r.URL.Query().Get("expires") == "") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		reply([]map[string]string{{"URI": f.srv.URL + "/cdn/" + parts[2]}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func link(modID, fileID int) string {
	return fmt.Sprintf("nxm://%s/mods/%d/files/%d?key=k&expires=1700000000&user_id=1", testDomain, modID, fileID)
}

// nexusApp — Modvault с игрой, подключённый к поддельному Nexus.
func nexusApp(t *testing.T) (*Manager, *fakeNexus, string) {
	t.Helper()
	a, home, g := newGame(t)
	if _, err := a.SetGame(g); err != nil {
		t.Fatal(err)
	}
	f := newFakeNexus(t)
	a.nexusBase = f.srv.URL
	return a, f, home
}

func login(t *testing.T, a *Manager) {
	t.Helper()
	if _, err := a.NexusLogin(context.Background(), " good\n"); err != nil {
		t.Fatal(err)
	}
}

func statusValue(s State, label string) string {
	for _, item := range s.Status {
		if item.Label == label {
			return item.Value
		}
	}
	return ""
}

func TestNexusLogin(t *testing.T) {
	a, f, home := nexusApp(t)
	ctx := context.Background()

	if v := statusValue(state(t, a), "Nexus"); v != "ключ не задан" {
		t.Errorf("до входа: %q", v)
	}
	if _, err := a.InstallLink(ctx, link(22, 1), nil); err == nil || !strings.Contains(err.Error(), "ключ Nexus не задан") {
		t.Errorf("загрузка без ключа: %v", err)
	}
	if _, err := a.NexusLogin(ctx, "bad"); err == nil {
		t.Error("плохой ключ принят")
	}
	if key, _ := a.keys.Load(); key != "" {
		t.Errorf("плохой ключ сохранён: %q", key)
	}

	login(t, a)
	if key, _ := a.keys.Load(); key != "good" {
		t.Errorf("ключ = %q", key)
	}
	if v := statusValue(state(t, a), "Nexus"); v != "zd" {
		t.Errorf("после входа: %q", v)
	}
	// Ключ не попадает в файлы программы.
	for path, data := range snapshot(t, home) {
		if strings.Contains(data, "good") {
			t.Errorf("ключ записан в %s", path)
		}
	}

	f.premium = true
	login(t, a)
	if v := statusValue(state(t, a), "Nexus"); v != "zd · Premium" {
		t.Errorf("Premium: %q", v)
	}
	if _, err := a.NexusLogout(); err != nil {
		t.Fatal(err)
	}
	if key, _ := a.keys.Load(); key != "" || a.NexusUser() != "" {
		t.Errorf("после выхода: ключ %q, имя %q", key, a.NexusUser())
	}
	if _, err := a.CheckUpdates(ctx, nil); err == nil {
		t.Error("проверка обновлений без ключа прошла")
	}
}

func TestInstallLink(t *testing.T) {
	a, f, home := nexusApp(t)
	ctx := context.Background()
	login(t, a)
	f.add(t, 22, "Scoreboard", fakeFile{ID: 100, Name: "Scoreboard", Version: "1.4.0", Category: "MAIN", FileName: `Score:board-22-1-4-0.zip`},
		map[string]string{"Scoreboard/Scoreboard.mod": "return {}", "Scoreboard/scripts/a.lua": strings.Repeat("-- a\n", 5000)})
	f.cut[100] = 2 // сеть рвётся дважды посреди файла

	var last Progress
	res, err := a.InstallLink(ctx, link(22, 100), func(p Progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Message, "Установлен: Scoreboard 1.4.0") {
		t.Errorf("сообщение: %q", res.Message)
	}
	m := findMod(t, res.State, "scoreboard")
	if !m.Enabled || m.Version != "1.4.0" || m.NexusID != 22 || m.Source != "Nexus Mods" || m.Files != 2 {
		t.Errorf("мод = %+v", m)
	}
	v, err := a.store.Get("scoreboard", "1.4.0")
	if err != nil || v.NexusFileID != 100 {
		t.Errorf("версия в хранилище: %+v, %v", v, err)
	}
	if last.Name != "Scoreboard" || last.Done != last.Total || last.Total != f.file(100).Size {
		t.Errorf("ход загрузки: %+v", last)
	}
	if left := snapshot(t, filepath.Join(home, downloadsDir)); len(left) != 0 {
		t.Errorf("в папке загрузок остались файлы: %v", left)
	}

	// Тот же файл второй раз: хранилище не меняется, архив не остаётся.
	if _, err := a.InstallLink(ctx, link(22, 100), nil); err == nil || !strings.Contains(err.Error(), "уже есть") {
		t.Errorf("повторная установка: %v", err)
	}
	if left := snapshot(t, filepath.Join(home, downloadsDir)); len(left) != 0 {
		t.Errorf("после повтора остались файлы: %v", left)
	}

	for _, bad := range []string{
		"nxm://skyrim/mods/22/files/100?key=k&expires=1",
		"nxm://" + testDomain + "/mods/22/files/999?key=k&expires=1",
		"nxm://" + testDomain + "/mods/22/files/100?key=old&expires=1",
		"https://example.com",
	} {
		if _, err := a.InstallLink(ctx, bad, nil); err == nil {
			t.Errorf("%s: ошибки нет", bad)
		}
	}
	// Без Premium и без ключа с сайта файл не отдаётся.
	if _, err := a.InstallLink(ctx, fmt.Sprintf("nxm://%s/mods/22/files/100", testDomain), nil); err == nil || !strings.Contains(err.Error(), "Mod Manager Download") {
		t.Errorf("ссылка без ключа: %v", err)
	}
}

func TestInstallFailureLeavesStoreAlone(t *testing.T) {
	a, f, home := nexusApp(t)
	ctx := context.Background()
	login(t, a)
	mod := map[string]string{"Flux/Flux.mod": "return {}"}
	f.add(t, 30, "Flux", fakeFile{ID: 300, Version: "1.0", Category: "MAIN"}, mod)
	f.add(t, 31, "Glow", fakeFile{ID: 310, Version: "1.0", Category: "MAIN"}, map[string]string{"Glow/Glow.mod": "return {}"})
	f.add(t, 32, "Junk", fakeFile{ID: 320, Version: "1.0", Category: "MAIN"}, map[string]string{"readme.txt": "не мод"})
	before := snapshot(t, filepath.Join(home, "mods"))
	profiles := snapshot(t, filepath.Join(home, "profiles"))

	f.broken[300] = true
	if _, err := a.InstallLink(ctx, link(30, 300), nil); err == nil {
		t.Error("сервер файлов не отвечает, а ошибки нет")
	}
	f.foreign[310] = true
	if _, err := a.InstallLink(ctx, link(31, 310), nil); err == nil || !strings.Contains(err.Error(), "не совпал") {
		t.Errorf("чужой архив: %v", err)
	}
	// Скачалось, но игра не знает, как это разложить: архив остаётся для разбора.
	_, err := a.InstallLink(ctx, link(32, 320), nil)
	if err == nil || !strings.Contains(err.Error(), "архив остался") {
		t.Errorf("непонятный архив: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.InstallLink(cancelled, link(30, 300), nil); err == nil {
		t.Error("отменённая загрузка прошла")
	}

	if !sameTree(before, snapshot(t, filepath.Join(home, "mods"))) {
		t.Error("неудачные загрузки изменили хранилище")
	}
	if !sameTree(profiles, snapshot(t, filepath.Join(home, "profiles"))) {
		t.Error("неудачные загрузки изменили профиль")
	}
	if left := snapshot(t, filepath.Join(home, "tmp")); len(left) != 0 {
		t.Errorf("в tmp остался мусор: %v", left)
	}

	// Сервер ожил: та же ссылка ставит мод.
	f.broken[300] = false
	res, err := a.InstallLink(ctx, link(30, 300), nil)
	if err != nil || ids(res.State) != "flux" {
		t.Errorf("после починки: %v, моды %s", err, ids(res.State))
	}
}

func TestUpdates(t *testing.T) {
	a, f, _ := nexusApp(t)
	ctx := context.Background()
	login(t, a)

	// Мод принят когда-то под своим названием и без номера файла; рядом —
	// мод с диска, о котором Nexus ничего не знает.
	if _, err := a.addArchive(writeZip(t, "Score Board-22-1-4-0-1700000000.zip", map[string]string{"Scoreboard/Scoreboard.mod": "return {}"})); err != nil {
		t.Fatal(err)
	}
	a.addArchive(writeZip(t, "Flux.zip", map[string]string{"Flux/Flux.mod": "return {}"}))
	f.add(t, 22, "Scoreboard", fakeFile{ID: 100, Version: "1.4.0", Category: "OLD_VERSION", Uploaded: 10}, map[string]string{"Scoreboard/Scoreboard.mod": "return {} -- 1.4"})
	f.add(t, 22, "Scoreboard", fakeFile{ID: 101, Version: "1.5.0", Category: "MAIN", Uploaded: 20}, map[string]string{"Scoreboard/Scoreboard.mod": "return {} -- 1.5"})
	f.add(t, 22, "Scoreboard", fakeFile{ID: 150, Name: "Scoreboard Skins", Version: "0.1", Category: "OPTIONAL", Uploaded: 30}, map[string]string{"Skins/Skins.mod": "return {}"})
	f.mods[22].updates = [][2]int{{100, 101}}

	// До первой проверки: мод с Nexus не проверен, окно проверит само при запуске.
	if s := state(t, a); findMod(t, s, "score_board").UpdateStatus != "unknown" || findMod(t, s, "flux").UpdateStatus != "" || !s.CheckOnStart {
		t.Errorf("до проверки: %+v, при запуске %v", findMod(t, s, "score_board"), s.CheckOnStart)
	}
	if s, _ := a.SetUpdateCheck(false); s.CheckOnStart {
		t.Error("проверка при запуске не выключилась")
	}
	if s, _ := a.SetUpdateCheck(true); !s.CheckOnStart {
		t.Error("проверка при запуске не включилась")
	}

	var steps []CheckProgress
	rep, err := a.CheckUpdates(ctx, func(p CheckProgress) { steps = append(steps, p) })
	if err != nil {
		t.Fatal(err)
	}
	// Ход проверки: сначала «спрашиваем об этом моде», потом «вот что узнали».
	if len(steps) != 3 {
		t.Fatalf("шагов проверки: %+v", steps)
	}
	if s := steps[0]; s.Total != 1 || strings.Join(s.Queued, ",") != "score_board" || len(s.Unchanged) != 0 || len(s.Mods) != 0 {
		t.Errorf("расклад проверки: %+v", s)
	}
	steps = steps[1:]
	if s := steps[0]; s.Finished || s.Done != 0 || s.Total != 1 || s.Name != "Score Board" || strings.Join(s.Mods, ",") != "score_board" {
		t.Errorf("шаг до запроса: %+v", s)
	}
	if s := steps[1]; !s.Finished || s.Done != 1 || s.Available != "1.5.0" || s.Missing {
		t.Errorf("шаг после ответа: %+v", s)
	}
	// Три запроса: категории игры (один раз), файлы мода и, один раз, его страница.
	if rep.Mods != 1 || rep.Unknown != 1 || rep.Updates != 1 || rep.Requests != 3 {
		t.Errorf("первая проверка: %+v", rep)
	}
	if m := findMod(t, rep.State, "score_board"); m.Author != "Автор Scoreboard" || m.AuthorURL != "https://www.nexusmods.com/users/220" {
		t.Errorf("автор после проверки: %+v", m)
	}
	if m := findMod(t, rep.State, "score_board"); m.Category != "User Interface" || m.Installed.IsZero() {
		t.Errorf("категория и время установки: %+v", m)
	}
	if m := findMod(t, rep.State, "score_board"); !m.HasStats || m.Endorsements != 2200 || m.Downloads != 110000 || m.UniqueDownloads != 66000 {
		t.Errorf("статистика после проверки: %+v", m)
	}
	if m := findMod(t, rep.State, "flux"); m.HasStats {
		t.Errorf("статистика мода с диска: %+v", m)
	}
	if page, err := a.AuthorPage("score_board"); err != nil || page != "https://www.nexusmods.com/users/220" {
		t.Errorf("профиль автора: %q, %v", page, err)
	}
	if _, err := a.AuthorPage("flux"); err == nil {
		t.Error("профиль автора мода с диска нашёлся")
	}
	if m := findMod(t, rep.State, "flux"); m.Author != "" {
		t.Errorf("автор мода с диска: %+v", m)
	}
	if !strings.Contains(rep.Message, "Есть обновление: 1") || !strings.Contains(rep.Message, "осталось: 2000") {
		t.Errorf("сообщение: %q", rep.Message)
	}
	if m := findMod(t, rep.State, "score_board"); m.Available != "1.5.0" || m.UpdateStatus != "update" {
		t.Errorf("доступная версия: %+v", m)
	}
	if m := findMod(t, state(t, a), "flux"); m.Available != "" || m.NexusID != 0 {
		t.Errorf("мод с диска: %+v", m)
	}

	// Вторая проверка спрашивает только «что менялось»: один запрос на всех.
	before := f.apiRequests()
	steps = nil
	rep, err = a.CheckUpdates(ctx, func(p CheckProgress) { steps = append(steps, p) })
	if len(steps) != 1 || steps[0].Total != 0 || strings.Join(steps[0].Unchanged, ",") != "score_board" {
		t.Errorf("расклад второй проверки: %+v", steps)
	}
	if err != nil || rep.Requests != 1 || f.apiRequests()-before != 1 || rep.Updates != 1 {
		t.Errorf("вторая проверка: %+v, %v, запросов %d", rep, err, f.apiRequests()-before)
	}

	// Без Premium обновление идёт через сайт.
	up, err := a.UpdateMod(ctx, "score_board", nil)
	if err != nil || up.URL != "https://www.nexusmods.com/"+testDomain+"/mods/22?tab=files&file_id=101&nmm=1" {
		t.Errorf("обновление без Premium: %+v, %v", up, err)
	}
	if _, err := a.UpdateMod(ctx, "flux", nil); err == nil {
		t.Error("обновление мода без номера на Nexus прошло")
	}
	if page, err := a.ModPage("score_board"); err != nil || !strings.HasSuffix(page, "/mods/22") {
		t.Errorf("страница мода: %q, %v", page, err)
	}

	// Ссылка с сайта обновляет тот же мод, а не заводит второй.
	res, err := a.InstallLink(ctx, link(22, 101), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids(res.State) != "score_board,flux" || !strings.Contains(res.Message, "Обновлён: Score Board до 1.5.0") {
		t.Errorf("после обновления: моды %s, сообщение %q", ids(res.State), res.Message)
	}
	if m := findMod(t, res.State, "score_board"); m.Version != "1.5.0" || m.Versions != 2 || m.Available != "" || !m.Enabled || m.UpdateStatus != "current" {
		t.Errorf("обновлённый мод: %+v", m)
	}

	// Дополнение со страницы того же мода — отдельный мод.
	res, err = a.InstallLink(ctx, link(22, 150), nil)
	if err != nil || ids(res.State) != "score_board,flux,scoreboard_skins" {
		t.Fatalf("дополнение: %v, моды %s", err, ids(res.State))
	}

	// Вышла ещё версия: с Premium она ставится без сайта, а из прежних
	// версий в хранилище остаётся одна.
	f.premium = true
	login(t, a)
	f.add(t, 22, "Scoreboard", fakeFile{ID: 102, Version: "1.6.0", Category: "MAIN", Uploaded: time.Now().Unix()}, map[string]string{"Scoreboard/Scoreboard.mod": "return {} -- 1.6"})
	f.file(101).Category = "OLD_VERSION"
	f.mods[22].updates = append(f.mods[22].updates, [2]int{101, 102})
	f.changed[22] = time.Now().Unix()
	if rep, err = a.CheckUpdates(ctx, nil); err != nil || rep.Updates != 1 || rep.Requests != 2 {
		t.Fatalf("третья проверка: %+v, %v", rep, err)
	}
	if m := findMod(t, rep.State, "scoreboard_skins"); m.Available != "" {
		t.Errorf("дополнению предложен основной файл: %+v", m)
	}
	up, err = a.UpdateMod(ctx, "score_board", nil)
	if err != nil || up.URL != "" {
		t.Fatalf("обновление с Premium: %+v, %v", up, err)
	}
	m := findMod(t, up.State, "score_board")
	if m.Version != "1.6.0" || m.Versions != 2 || m.Available != "" {
		t.Errorf("после второго обновления: %+v", m)
	}
	if _, err := a.store.Get("score_board", "1.5.0"); err != nil {
		t.Errorf("прежняя версия не осталась: %v", err)
	}
	if _, err := a.store.Get("score_board", "1.4.0"); err == nil {
		t.Error("позапрошлая версия осталась в хранилище")
	}
}

// Обновление не спрашивает, развёртывать ли: включённый мод остаётся в
// игре новой версией, выключенный остаётся выключенным.
func TestUpdateKeepsModState(t *testing.T) {
	a, f, _ := nexusApp(t)
	ctx := context.Background()
	login(t, a)
	g := a.GameDir()
	inGame := func() string { return snapshot(t, g)["mods/Flux/Flux.mod"] }
	planned := func() string { return state(t, a).PlanTitle }
	for i, version := range []string{"1.0", "2.0", "3.0", "4.0"} {
		f.add(t, 30, "Flux", fakeFile{ID: 300 + i, Version: version, Category: "MAIN"}, map[string]string{"Flux/Flux.mod": "return {} -- " + version})
	}
	f.add(t, 31, "Glow", fakeFile{ID: 310, Version: "1.0", Category: "MAIN"}, map[string]string{"Glow/Glow.mod": "return {}"})

	// Новый мод сам в игру не идёт: его развёртывает пользователь.
	res, err := a.InstallLink(ctx, link(30, 300), nil)
	if err != nil || inGame() != "" || !strings.Contains(res.Message, "«Развернуть»") {
		t.Fatalf("новый мод: %v, в игре %q, сообщение %q", err, inGame(), res.Message)
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}

	// Включённый и развёрнутый мод после обновления сразу в игре.
	res, err = a.InstallLink(ctx, link(30, 301), nil)
	if err != nil || inGame() != "return {} -- 2.0" || planned() != "" || !strings.Contains(res.Message, "уже в игре") {
		t.Errorf("обновление включённого: %v, в игре %q, план %q, сообщение %q", err, inGame(), planned(), res.Message)
	}
	if m := findMod(t, res.State, "flux"); !m.Enabled || m.State != "Развёрнут" {
		t.Errorf("после обновления: %+v", m)
	}

	// В профиле ждёт чужое изменение: без спроса его не развёртываем.
	if _, err := a.InstallLink(ctx, link(31, 310), nil); err != nil {
		t.Fatal(err)
	}
	res, err = a.InstallLink(ctx, link(30, 302), nil)
	if err != nil || inGame() != "return {} -- 2.0" || planned() == "" || !strings.Contains(res.Message, "ждут и другие изменения") {
		t.Errorf("обновление при чужих изменениях: %v, в игре %q, сообщение %q", err, inGame(), res.Message)
	}
	if _, ok := snapshot(t, g)["mods/Glow/Glow.mod"]; ok {
		t.Error("чужое изменение развёрнуто без спроса")
	}

	// Выключенный мод остаётся выключенным, и в игре его нет.
	if _, err := a.SetEnabled("flux", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	res, err = a.InstallLink(ctx, link(30, 303), nil)
	if err != nil || inGame() != "" || planned() != "" || !strings.Contains(res.Message, "остаётся выключенным") {
		t.Errorf("обновление выключенного: %v, в игре %q, план %q, сообщение %q", err, inGame(), planned(), res.Message)
	}
	if m := findMod(t, res.State, "flux"); m.Enabled || m.Version != "4.0" {
		t.Errorf("выключенный мод после обновления: %+v", m)
	}
}

// fakeProtocol — обработчик ссылок вместо реестра Windows.
type fakeProtocol struct{ cmd string }

func (p *fakeProtocol) Command() (string, error)    { return p.cmd, nil }
func (p *fakeProtocol) SetCommand(cmd string) error { p.cmd = cmd; return nil }

func TestToggleNxm(t *testing.T) {
	a, _, _ := nexusApp(t)
	if v := statusValue(state(t, a), "Ссылки nxm"); v != "" {
		t.Errorf("без доступа к системе пункт показан: %q", v)
	}
	if _, err := a.ToggleNxm(`C:\m.exe`); err == nil {
		t.Error("переключение без доступа к системе прошло")
	}

	vortex := `"G:\Vortex\Vortex.exe" -d "%1"`
	p := &fakeProtocol{cmd: vortex}
	a.protocol = p
	if v := statusValue(state(t, a), "Ссылки nxm"); v != "открывает Vortex" {
		t.Errorf("до переключения: %q", v)
	}
	s, err := a.ToggleNxm(`C:\Program Files\Modvault\modvault-gui.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if p.cmd != `"C:\Program Files\Modvault\modvault-gui.exe" "%1"` || statusValue(s, "Ссылки nxm") != "открывает Modvault" {
		t.Errorf("после включения: %q, %q", p.cmd, statusValue(s, "Ссылки nxm"))
	}
	if owner, ours := a.NxmOwner(); !ours || owner != "Modvault" {
		t.Errorf("владелец: %q %v", owner, ours)
	}
	// Vortex забрал ссылки сам: Modvault это видит и не считает их своими.
	p.cmd = vortex
	if _, ours := a.NxmOwner(); ours {
		t.Error("чужой обработчик принят за свой")
	}
	if _, err := a.ToggleNxm(`C:\m.exe`); err != nil {
		t.Fatal(err)
	}
	if s, err = a.ToggleNxm(""); err != nil || p.cmd != vortex || statusValue(s, "Ссылки nxm") != "открывает Vortex" {
		t.Errorf("после выключения: %q, %v", p.cmd, err)
	}

	// Никто не открывал — после выключения снова никто.
	p.cmd = ""
	a.ToggleNxm(`C:\m.exe`)
	if s, _ = a.ToggleNxm(""); p.cmd != "" || statusValue(s, "Ссылки nxm") != "никто не открывает" {
		t.Errorf("пустой обработчик: %q, %q", p.cmd, statusValue(s, "Ссылки nxm"))
	}
}

func TestCleanDownloads(t *testing.T) {
	a, home := newApp(t)
	dir := filepath.Join(home, downloadsDir)
	os.MkdirAll(dir, 0o755)
	old := time.Now().Add(-partAge - time.Hour)
	for name, at := range map[string]time.Time{
		"1-old.zip.part":   old,
		"2-fresh.zip.part": time.Now(),
		"3-kept.zip":       old, // архив мода, который не удалось добавить
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("x"), 0o644)
		os.Chtimes(path, at, at)
	}
	NewWith(home, a.game) // уборка — при запуске
	left := snapshot(t, dir)
	if _, ok := left["1-old.zip.part"]; ok || len(left) != 2 {
		t.Errorf("после уборки в загрузках: %v", left)
	}
}
