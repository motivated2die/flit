package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil || cfg.Language != "de" || !cfg.KeepAudio {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Default()
	in.Language = "en"
	in.Vocabulary = []string{"flit", "Parakeet"}
	in.Providers = map[string]bool{"discord": true}
	if err := Save(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Language != "en" || len(out.Vocabulary) != 2 || !out.ProviderEnabled("discord", false) || out.ProviderEnabled("teams", false) {
		t.Fatalf("%+v", out)
	}
	if _, err := os.Stat(Path(dir) + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left")
	}
}

func TestOlderFileKeepsNewDefaults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte(`{"language":"en"}`), 0o644)
	cfg, _ := Load(dir)
	if !cfg.KeepAudio {
		t.Fatal("a field missing from an older file lost its default")
	}
}

func TestBrokenFileIsKept(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte(`{"language": "en",`), 0o644)
	cfg, err := Load(dir)
	if err == nil || cfg.Language != "de" {
		t.Fatalf("%+v %v", cfg, err)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "config.broken-*.json"))
	if len(m) != 1 {
		t.Fatalf("backups %v", m)
	}
	if b, _ := os.ReadFile(m[0]); !strings.Contains(string(b), `"en"`) {
		t.Fatal("backup lost the content")
	}
}
