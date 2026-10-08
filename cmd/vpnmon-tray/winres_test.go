package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// O walk exige o comctl32 v6: sem o manifest, a bandeja não abre. O
// winres.json versionado é a fonte do .syso gerado no build.
func TestWinresManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("winres", "winres.json"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Icon     map[string]map[string]string `json:"RT_GROUP_ICON"`
		Manifest map[string]map[string]struct {
			ExecutionLevel string `json:"execution-level"`
			DPIAwareness   string `json:"dpi-awareness"`
			CommonControls bool   `json:"use-common-controls-v6"`
		} `json:"RT_MANIFEST"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	m := w.Manifest["#1"]["0409"]
	if !m.CommonControls || m.ExecutionLevel != "as invoker" || m.DPIAwareness != "per monitor v2" {
		t.Fatalf("manifest: %+v", m)
	}
	icon := w.Icon["APP"]["0000"]
	if _, err := os.Stat(filepath.Join("winres", icon)); err != nil {
		t.Fatalf("ícone do exe: %v", err)
	}
}
