package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// PkgCover é a cobertura de um pacote (instruções cobertas / total).
type PkgCover struct {
	Pkg            string
	Covered, Total int
}

// Percent devolve a porcentagem (100 sem instruções).
func (p PkgCover) Percent() float64 {
	if p.Total == 0 {
		return 100
	}
	return 100 * float64(p.Covered) / float64(p.Total)
}

// Parse lê um perfil de `go test -coverprofile` e soma por pacote. Um bloco
// repetido (o mesmo trecho medido por mais de um binário de teste) conta uma
// vez, coberto se algum o cobriu.
func Parse(r io.Reader) ([]PkgCover, PkgCover, error) {
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]block{}
	sc := bufio.NewScanner(r)
	first := true
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			switch strings.TrimPrefix(line, "mode: ") {
			case "set", "count", "atomic":
				if strings.HasPrefix(line, "mode: ") {
					continue
				}
			}
			return nil, PkgCover{}, fmt.Errorf("linha 1: esperava \"mode: set|count|atomic\", veio %q", line)
		}
		if line == "" {
			continue
		}
		// arquivo.go:1.2,3.4 stmts count
		f := strings.Fields(line)
		if len(f) != 3 || !strings.Contains(f[0], ":") {
			return nil, PkgCover{}, fmt.Errorf("linha %d malformada: %q", n, line)
		}
		stmts, err1 := strconv.Atoi(f[1])
		count, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || stmts < 0 || count < 0 {
			return nil, PkgCover{}, fmt.Errorf("linha %d malformada: %q", n, line)
		}
		// Arquivos _windows.go ficam de fora do piso (§10.1), em qualquer SO.
		if file := f[0][:strings.LastIndex(f[0], ":")]; strings.HasSuffix(file, "_windows.go") {
			continue
		}
		b := blocks[f[0]]
		b.stmts = stmts
		b.covered = b.covered || count > 0
		blocks[f[0]] = b
	}
	if err := sc.Err(); err != nil {
		return nil, PkgCover{}, err
	}
	if first {
		return nil, PkgCover{}, fmt.Errorf("perfil vazio")
	}
	if len(blocks) == 0 {
		return nil, PkgCover{}, fmt.Errorf("perfil sem instruções")
	}
	byPkg := map[string]*PkgCover{}
	total := PkgCover{Pkg: "total"}
	for key, b := range blocks {
		file := key[:strings.LastIndex(key, ":")]
		pkg := path.Dir(file)
		p := byPkg[pkg]
		if p == nil {
			p = &PkgCover{Pkg: pkg}
			byPkg[pkg] = p
		}
		p.Total += b.stmts
		total.Total += b.stmts
		if b.covered {
			p.Covered += b.stmts
			total.Covered += b.stmts
		}
	}
	out := make([]PkgCover, 0, len(byPkg))
	for _, p := range byPkg {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg < out[j].Pkg })
	return out, total, nil
}

// Markdown monta a tabela do resumo do job, com o prefixo do módulo cortado.
func Markdown(pkgs []PkgCover, total PkgCover, module string, min float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Cobertura (mínimo %.0f %% no total)\n\n| Pacote | Cobertura |\n|---|---:|\n", min)
	for _, p := range pkgs {
		name := strings.TrimPrefix(p.Pkg, module+"/")
		if name == module {
			name = "."
		}
		fmt.Fprintf(&b, "| `%s` | %.1f %% |\n", name, p.Percent())
	}
	fmt.Fprintf(&b, "| **total** | **%.1f %%** |\n", total.Percent())
	return b.String()
}
