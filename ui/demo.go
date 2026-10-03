package ui

// demoMod — мод из демонстрационного набора. Набор исчезнет, когда окно
// начнёт получать данные из хранилища и профиля (этапы 2–3).
type demoMod struct {
	id, name     string
	version      string
	available    string // новая версия на Nexus, если есть
	source       string
	files        int
	dependsOn    string
	conflictWith string // id мода, с которым делит файл
	enabled      bool   // включён в профиле
	deployed     bool   // сейчас лежит в игре
	pinned       bool   // выключить нельзя
}

func demoMods() []demoMod {
	const dmf = "Darktide Mod Framework"
	return []demoMod{
		{id: "dmf", name: dmf, version: "25.03", source: "Nexus Mods", files: 212, enabled: true, deployed: true, pinned: true},
		{id: "animation_events", name: "animation_events", version: "1.0.2", source: "Nexus Mods", files: 3, dependsOn: dmf, enabled: true, deployed: true},
		{id: "scoreboard", name: "Scoreboard", version: "1.4.0", available: "1.5.0", source: "Nexus Mods", files: 14, dependsOn: dmf, enabled: true, deployed: true},
		{id: "numeric_ui", name: "Numeric UI", version: "2.1.0", source: "Nexus Mods", files: 9, dependsOn: dmf, enabled: true, deployed: true},
		{id: "healthbars", name: "Healthbars", version: "1.2.1", source: "Nexus Mods", files: 6, dependsOn: dmf, conflictWith: "numeric_ui", enabled: true, deployed: true},
		{id: "spidey_sense", name: "Spidey Sense", version: "1.1.0", source: "Архив с диска", files: 5, dependsOn: dmf, enabled: false, deployed: true},
	}
}
