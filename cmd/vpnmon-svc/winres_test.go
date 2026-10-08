package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// O vpnmon-svc é console (serviço + CLI): manifest asInvoker — a CLI confere
// a elevação sozinha e `version` roda sem ela — e versão nos recursos. O
// winres.json versionado é a fonte do .syso gerado no build.
func TestWinresManifestAndVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("winres", "winres.json"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Icon     map[string]map[string]string `json:"RT_GROUP_ICON"`
		Manifest map[string]map[string]struct {
			ExecutionLevel string `json:"execution-level"`
			CommonControls bool   `json:"use-common-controls-v6"`
		} `json:"RT_MANIFEST"`
		Version map[string]map[string]struct {
			Info map[string]map[string]string `json:"info"`
		} `json:"RT_VERSION"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	if m := w.Manifest["#1"]["0409"]; m.ExecutionLevel != "as invoker" || m.CommonControls {
		t.Fatalf("manifest: %+v", m)
	}
	info := w.Version["#1"]["0000"].Info["0409"]
	if info["OriginalFilename"] != "vpnmon-svc.exe" || info["ProductName"] != "VPN Monitor" {
		t.Fatalf("versão: %+v", info)
	}
	icon := w.Icon["APP"]["0000"]
	if _, err := os.Stat(filepath.Join("winres", icon)); err != nil {
		t.Fatalf("ícone do exe: %v", err)
	}
}
