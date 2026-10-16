package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bardraw.json")
	old := `{"svg_path": "mine", "game_profile": "bar", "quality": 4, "time_budget_s": 50, "_help": {"x": "y"}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SVGPath != "mine" || cfg.Quality != 7 || !cfg.TrayIcon || cfg.HUDScale != 1 ||
		cfg.TimeBudgetS != 60 || !cfg.AutoLowerQuality || cfg.ConfigVersion != configVersion {
		t.Fatalf("%+v", cfg)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "game_profile") || !strings.Contains(string(data), `"tray_icon": true`) ||
		!strings.Contains(string(data), `"svg_path": "mine"`) || !strings.Contains(string(data), `"time_budget_s": 60`) {
		t.Fatalf("not migrated:\n%s", data)
	}
	if !strings.Contains(string(data), `"quality": 7`) || !strings.Contains(string(data), `"time_budget_s": 60`) {
		t.Fatalf("old defaults not updated:\n%s", data)
	}
	// an up-to-date file is left alone
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); !st.ModTime().Equal(past) {
		t.Fatal("current config was rewritten")
	}

	// a deliberately chosen budget in a current file is kept
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"time_budget_s": 60`, `"time_budget_s": 50`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(path); cfg.TimeBudgetS != 50 {
		t.Fatalf("user budget overwritten: %v", cfg.TimeBudgetS)
	}
}

func TestConfigKeepsCustomValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bardraw.json")
	if err := os.WriteFile(path, []byte(`{"quality": 5, "time_budget_s": 45}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil || cfg.Quality != 5 || cfg.TimeBudgetS != 45 {
		t.Fatalf("%v %d %v", err, cfg.Quality, cfg.TimeBudgetS)
	}
}
