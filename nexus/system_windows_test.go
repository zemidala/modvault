package nexus

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestCredential(t *testing.T) {
	c := credential{target: fmt.Sprintf("Modvault: проверка %d", os.Getpid())}
	t.Cleanup(func() { c.Delete() })

	if key, err := c.Load(); err != nil || key != "" {
		t.Fatalf("до записи: %q, %v", key, err)
	}
	if err := c.Save("ключ-123"); err != nil {
		t.Fatal(err)
	}
	if key, err := c.Load(); err != nil || key != "ключ-123" {
		t.Errorf("после записи: %q, %v", key, err)
	}
	if err := c.Save("другой"); err != nil {
		t.Fatal(err)
	}
	if key, _ := c.Load(); key != "другой" {
		t.Errorf("после замены: %q", key)
	}
	if err := c.Delete(); err != nil {
		t.Fatal(err)
	}
	if key, err := c.Load(); err != nil || key != "" {
		t.Errorf("после удаления: %q, %v", key, err)
	}
	if err := c.Delete(); err != nil {
		t.Errorf("повторное удаление: %v", err)
	}
}

func TestProtocol(t *testing.T) {
	// Настоящий обработчик nxm не трогаем: проверяем на своём ключе.
	path := fmt.Sprintf(`Software\Classes\modvault-test-%d`, os.Getpid())
	p := protocol{path: path}
	t.Cleanup(func() {
		for _, sub := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ``} {
			registry.DeleteKey(registry.CURRENT_USER, path+sub)
		}
	})

	if cmd, err := p.Command(); err != nil || cmd != "" {
		t.Fatalf("до записи: %q, %v", cmd, err)
	}
	want := `"C:\Program Files\Modvault\modvault-gui.exe" "%1"`
	if err := p.SetCommand(want); err != nil {
		t.Fatal(err)
	}
	if cmd, err := p.Command(); err != nil || cmd != want {
		t.Errorf("после записи: %q, %v", cmd, err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.GetStringValue("URL Protocol"); err != nil {
		t.Errorf("ключ не помечен как протокол: %v", err)
	}
	k.Close()

	if err := p.SetCommand(""); err != nil {
		t.Fatal(err)
	}
	if cmd, err := p.Command(); err != nil || cmd != "" {
		t.Errorf("после снятия: %q, %v", cmd, err)
	}
	if err := p.SetCommand(""); err != nil {
		t.Errorf("повторное снятие: %v", err)
	}
}
