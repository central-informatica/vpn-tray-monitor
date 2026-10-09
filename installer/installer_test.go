// Package installer guarda o MSI (Product.wxs, WiX v5). O WiX só roda no
// Windows (job msi do CI); este teste roda em qualquer lugar e amarra o .wxs
// às constantes que o código também usa, para MSI e serviço não divergirem.
package installer

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
)

const (
	nsWix  = "http://wixtoolset.org/schemas/v4/wxs"
	nsUtil = "http://wixtoolset.org/schemas/v4/wxs/util"
	// UpgradeCode nunca muda: é o que liga todas as versões no MajorUpgrade.
	upgradeCode = "3A0A8DBB-61E5-4653-AFE7-63C4D3EF3ACF"
)

// node é um elemento XML genérico.
type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []node     `xml:",any"`
}

func (n node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

// all devolve, em ordem de documento, os elementos com esse nome local.
func (n node) all(space, local string) []node {
	var out []node
	var walk func(node)
	walk = func(c node) {
		if c.XMLName.Space == space && c.XMLName.Local == local {
			out = append(out, c)
		}
		for _, k := range c.Children {
			walk(k)
		}
	}
	walk(n)
	return out
}

func (n node) one(t *testing.T, space, local string) node {
	t.Helper()
	got := n.all(space, local)
	if len(got) != 1 {
		t.Fatalf("esperava 1 <%s>, achei %d", local, len(got))
	}
	return got[0]
}

func load(t *testing.T) node {
	t.Helper()
	data, err := os.ReadFile("Product.wxs")
	if err != nil {
		t.Fatal(err)
	}
	var root node
	if err := xml.Unmarshal(data, &root); err != nil {
		t.Fatalf("Product.wxs não é XML válido: %v", err)
	}
	if root.XMLName.Space != nsWix || root.XMLName.Local != "Wix" {
		t.Fatalf("raiz %v", root.XMLName)
	}
	return root
}

func TestPackage(t *testing.T) {
	p := load(t).one(t, nsWix, "Package")
	want := map[string]string{
		"Name": "VPN Monitor", "Version": "$(var.ProductVersion)", "UpgradeCode": upgradeCode,
		"Manufacturer": "$(var.Manufacturer)", "Scope": "perMachine", "InstallerVersion": "500",
	}
	for k, v := range want {
		if got := p.attr(k); got != v {
			t.Errorf("Package/@%s = %q, quer %q", k, got, v)
		}
	}
	if mu := p.one(t, nsWix, "MajorUpgrade"); mu.attr("DowngradeErrorMessage") == "" || mu.attr("AllowDowngrades") != "" {
		t.Errorf("MajorUpgrade deve bloquear downgrade: %+v", mu.Attrs)
	}
	// A bandeja não é fechada no upgrade (decisão do Marco C): exe em uso é
	// trocado no reboot.
	rm := ""
	for _, pr := range p.all(nsWix, "Property") {
		if pr.attr("Id") == "MSIRESTARTMANAGERCONTROL" {
			rm = pr.attr("Value")
		}
	}
	if rm != "Disable" {
		t.Errorf("MSIRESTARTMANAGERCONTROL %q, quer Disable", rm)
	}
	const osCond = "Installed OR (VersionNT64 AND WINBUILD >= 17763)"
	if lc := p.one(t, nsWix, "Launch"); lc.attr("Condition") != osCond {
		t.Errorf("condição de SO %q, quer %q", lc.attr("Condition"), osCond)
	}
}

func TestServiceMatchesCode(t *testing.T) {
	root := load(t)
	si := root.one(t, nsWix, "ServiceInstall")
	want := map[string]string{
		"Name": svc.ServiceName, "DisplayName": svc.DisplayName, "Description": svc.Description,
		"Start": "auto", "Account": "LocalSystem", "Type": "ownProcess", "Vital": "yes",
		// Igual ao mgr.ErrorNormal de svc.installNamed (vpnmon-svc install).
		"ErrorControl": "normal",
	}
	for k, v := range want {
		if got := si.attr(k); got != v {
			t.Errorf("ServiceInstall/@%s = %q, quer %q", k, got, v)
		}
	}
	var deps []string
	for _, d := range si.all(nsWix, "ServiceDependency") {
		deps = append(deps, d.attr("Id"))
	}
	if !slices.Equal(deps, svc.Dependencies()) {
		t.Errorf("dependências %v, quer %v", deps, svc.Dependencies())
	}
	sc := root.one(t, nsWix, "ServiceControl")
	for k, v := range map[string]string{"Name": svc.ServiceName, "Start": "install", "Stop": "both", "Remove": "uninstall", "Wait": "yes"} {
		if got := sc.attr(k); got != v {
			t.Errorf("ServiceControl/@%s = %q, quer %q", k, got, v)
		}
	}
	// Tabelas MsiServiceConfig*: documentadas como quebradas; a política do
	// SCM vem do serviço (svc.EnsurePolicy).
	for _, el := range []string{"ServiceConfig", "ServiceConfigFailureActions"} {
		if n := len(root.all(nsWix, el)) + len(root.all(nsUtil, el)); n != 0 {
			t.Errorf("<%s> não deve ser usado (%d)", el, n)
		}
	}
}

func TestDataFolderACLMatchesService(t *testing.T) {
	root := load(t)
	pe := root.one(t, nsWix, "PermissionEx")
	// Só a DACL do serviço, sem dono: o MSI não pode trocar o dono de uma
	// pasta pré-criada por usuário comum (o serviço a põe em quarentena, §5.1).
	i := strings.Index(acl.DirSDDL, "D:")
	if i < 0 {
		t.Fatalf("acl.DirSDDL sem DACL: %q", acl.DirSDDL)
	}
	if got, want := pe.attr("Sddl"), acl.DirSDDL[i:]; got != want {
		t.Fatalf("Sddl %q, quer só a DACL do serviço %q (sem O:)", got, want)
	}
	var dataDir node
	for _, d := range root.all(nsWix, "Directory") {
		if d.attr("Id") == "DATAFOLDER" {
			dataDir = d
		}
	}
	if dataDir.attr("Name") != config.DataDirName {
		t.Fatalf("pasta de dados %q, o serviço usa %q", dataDir.attr("Name"), config.DataDirName)
	}
}

func TestEventSourceMatchesService(t *testing.T) {
	es := load(t).one(t, nsUtil, "EventSource")
	if es.attr("Name") != logging.EventSourceName || es.attr("Log") != "Application" {
		t.Fatalf("EventSource %+v", es.Attrs)
	}
	// Mesma DLL de mensagens do eventlog.InstallAsEventCreate do install.
	if !strings.HasSuffix(es.attr("EventMessageFile"), `\System32\EventCreate.exe`) {
		t.Fatalf("EventMessageFile %q", es.attr("EventMessageFile"))
	}
}

func TestSeedMatchesService(t *testing.T) {
	root := load(t)
	secure := map[string]bool{}
	for _, p := range root.all(nsWix, "Property") {
		if p.attr("Secure") == "yes" {
			secure[p.attr("Id")] = true
		}
	}
	var key node
	for _, k := range root.all(nsWix, "RegistryKey") {
		if k.attr("Root") == "HKLM" && k.attr("Key") == config.SeedRegistryPath {
			key = k
		}
	}
	var names []string
	for _, v := range key.all(nsWix, "RegistryValue") {
		names = append(names, v.attr("Name"))
		if v.attr("Value") != "["+v.attr("Name")+"]" || v.attr("Type") != "string" {
			t.Errorf("valor %s: Value=%q Type=%q", v.attr("Name"), v.attr("Value"), v.attr("Type"))
		}
		if !secure[v.attr("Name")] {
			t.Errorf("propriedade %s sem Secure=\"yes\"", v.attr("Name"))
		}
	}
	if !slices.Equal(names, config.SeedValueNames()) {
		t.Fatalf("valores do seed %v, o serviço lê %v", names, config.SeedValueNames())
	}
	if !secure["PURGE"] {
		t.Error("PURGE sem Secure=\"yes\"")
	}
	// Nenhuma senha no MSI.
	for _, p := range root.all(nsWix, "Property") {
		if id := strings.ToUpper(p.attr("Id")); strings.Contains(id, "PASS") || strings.Contains(id, "SENHA") {
			t.Errorf("propriedade de senha no MSI: %s", p.attr("Id"))
		}
	}
}

func TestPurgeOnlyOnExplicitUninstall(t *testing.T) {
	root := load(t)
	rf := root.one(t, nsUtil, "RemoveFolderEx")
	const purgeCond = `PURGE=1 AND REMOVE~="ALL" AND NOT UPGRADINGPRODUCTCODE`
	if cond := rf.attr("Condition"); cond != purgeCond {
		t.Errorf("condição do RemoveFolderEx %q, quer %q", cond, purgeCond)
	}
	// A propriedade de busca tem de ser pública (WIX0012), mas não Secure:
	// assim, numa desinstalação gerenciada, a linha de comando do msiexec não
	// consegue apontar o expurgo para outra pasta.
	for _, p := range root.all(nsWix, "Property") {
		if p.attr("Id") == rf.attr("Property") && p.attr("Secure") != "" {
			t.Errorf("propriedade %s do expurgo não pode ser Secure", p.attr("Id"))
		}
	}
	if rf.attr("On") != "uninstall" {
		t.Errorf("RemoveFolderEx/@On %q", rf.attr("On"))
	}
	// A pasta vem do registro (a CA roda antes de as pastas resolverem).
	var search node
	for _, p := range root.all(nsWix, "Property") {
		if p.attr("Id") == rf.attr("Property") {
			search = p.one(t, nsWix, "RegistrySearch")
		}
	}
	if search.attr("Key") != `SOFTWARE\VPNMonitor` || search.attr("Name") != "DataDir" {
		t.Fatalf("RegistrySearch da pasta: %+v", search.Attrs)
	}
}

func TestTrayStartsOnLogon(t *testing.T) {
	root := load(t)
	found := false
	for _, v := range root.all(nsWix, "RegistryValue") {
		if v.attr("Root") == "HKLM" && v.attr("Key") == `SOFTWARE\Microsoft\Windows\CurrentVersion\Run` {
			found = v.attr("Value") == `"[#TrayExe]"`
		}
	}
	if !found {
		t.Fatal("bandeja fora do HKLM Run (ou caminho sem aspas)")
	}
	if sc := root.one(t, nsWix, "Shortcut"); sc.attr("Directory") != "ProgramMenuFolder" {
		t.Fatalf("atalho em %q", sc.attr("Directory"))
	}
}

// §9: nenhuma custom action própria; do util, só a origem do Event Log
// (registro) e o RemoveFolderEx (a única CA, do próprio WiX).
func TestNoCustomActions(t *testing.T) {
	root := load(t)
	for _, el := range []string{"CustomAction", "CustomActionRef", "SetProperty", "SetDirectory", "Binary", "InstallExecuteSequence", "InstallUISequence"} {
		if n := len(root.all(nsWix, el)); n != 0 {
			t.Errorf("<%s> proibido (%d)", el, n)
		}
	}
	allowed := map[string]bool{"EventSource": true, "RemoveFolderEx": true}
	var walk func(node)
	walk = func(n node) {
		if n.XMLName.Space == nsUtil && !allowed[n.XMLName.Local] {
			t.Errorf("util:%s não permitido", n.XMLName.Local)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}

// Um arquivo por componente, chave = o próprio arquivo: com Guid automático o
// WiX só aceita vários arquivos se a chave for versionada e os demais não
// (WIX0367), e LICENCA.txt/README.md não têm versão. Um por componente também
// é a regra do Windows Installer para o GUID não mudar entre versões.
func TestOneFilePerComponent(t *testing.T) {
	for _, c := range load(t).all(nsWix, "Component") {
		files := c.all(nsWix, "File")
		if len(files) > 1 {
			t.Errorf("componente %q tem %d arquivos, quer no máximo 1", c.attr("Id"), len(files))
		}
		if len(files) == 1 && files[0].attr("KeyPath") != "yes" {
			t.Errorf("componente %q: o arquivo %q deve ser a chave", c.attr("Id"), files[0].attr("Id"))
		}
		if g := c.attr("Guid"); g != "" && g != "*" {
			t.Errorf("componente %q com Guid fixo %q; quer automático", c.attr("Id"), g)
		}
	}
}

// O fabricante aparece em três lugares que o build não amarra: o define do
// Product.wxs e o CompanyName dos recursos dos dois exes.
func TestManufacturerMatchesExeResources(t *testing.T) {
	data, err := os.ReadFile("Product.wxs")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<\?define\s+Manufacturer\s*=\s*"([^"]*)"\s*\?>`).FindSubmatch(data)
	if m == nil {
		t.Fatal("Product.wxs sem <?define Manufacturer = \"…\" ?>")
	}
	want := string(m[1])
	if want == "" {
		t.Fatal("fabricante vazio")
	}
	for _, exe := range []string{"vpnmon-svc", "vpnmon-tray"} {
		path := filepath.Join("..", "cmd", exe, "winres", "winres.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var res struct {
			Version map[string]map[string]struct {
				Info map[string]struct {
					CompanyName string
				} `json:"info"`
			} `json:"RT_VERSION"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		n := 0
		for _, langs := range res.Version {
			for _, lang := range langs {
				for _, info := range lang.Info {
					n++
					if info.CompanyName != want {
						t.Errorf("%s: CompanyName %q, o MSI usa %q", path, info.CompanyName, want)
					}
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: nenhum bloco de versão com CompanyName", path)
		}
	}
}
