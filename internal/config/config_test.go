package config

import (
	"os"
	"path/filepath"
	"testing"
)

const baseValidConfig = `
server:
  listen_address: "127.0.0.1:8087"
database:
  path: "data/cbc.db"
cbs:
  default_message_identifier: 0x1112
cbe:
  address: "cbe.example.com:5222"
  domain: "cbe.example.com"
  username: "cbc"
  password: "secret"
`

func loadYAML(t *testing.T, yaml string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cbc.yaml")
	if err := os.WriteFile(path, []byte(baseValidConfig+yaml), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestCellInventoryDisabledByDefault(t *testing.T) {
	c, err := loadYAML(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.CellInventory.Enabled {
		t.Fatal("cell_inventory must default to disabled when absent from config")
	}
}

func TestCellInventoryDefaultsWhenEnabled(t *testing.T) {
	c, err := loadYAML(t, "cell_inventory:\n  enabled: true\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.CellInventory.MaxImportSizeBytes != 10*1024*1024 {
		t.Fatalf("max_import_size_bytes=%d", c.CellInventory.MaxImportSizeBytes)
	}
	if c.CellInventory.DefaultImportMode != "validate-only" {
		t.Fatalf("default_import_mode=%q", c.CellInventory.DefaultImportMode)
	}
}

func TestCellInventoryRejectsInvalidDefaultImportMode(t *testing.T) {
	_, err := loadYAML(t, "cell_inventory:\n  enabled: true\n  default_import_mode: \"bogus\"\n")
	if err == nil {
		t.Fatal("expected an invalid default_import_mode to fail validation")
	}
}
