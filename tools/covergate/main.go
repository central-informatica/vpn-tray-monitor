// Comando covergate aplica o piso de cobertura do CI (§10.1) a um perfil de
// `go test -coverprofile` e imprime a tabela por pacote em Markdown (para o
// resumo do job). Sai com 1 se o total ficar abaixo do mínimo.
//
// Uso: go run ./tools/covergate -min 80 -profile coverage.out [-summary arquivo]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const module = "github.com/guibsu/vpn-tray-monitor"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("covergate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minPct := fs.Float64("min", 80, "cobertura mínima do total, em %")
	profile := fs.String("profile", "coverage.out", "perfil de cobertura")
	summary := fs.String("summary", "", "acrescenta a tabela a este arquivo (ex.: $GITHUB_STEP_SUMMARY)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "uso: covergate [-min 80] [-profile coverage.out] [-summary arquivo]")
		return 2
	}
	f, err := os.Open(*profile)
	if err != nil {
		fmt.Fprintln(stderr, "erro:", err)
		return 1
	}
	defer f.Close()
	pkgs, total, err := Parse(f)
	if err != nil {
		fmt.Fprintf(stderr, "erro: %s: %v\n", *profile, err)
		return 1
	}
	table := Markdown(pkgs, total, module, *minPct)
	fmt.Fprint(stdout, table)
	if *summary != "" {
		s, err := os.OpenFile(*summary, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintln(stderr, "erro:", err)
			return 1
		}
		_, werr := io.WriteString(s, table)
		if cerr := s.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			fmt.Fprintln(stderr, "erro:", werr)
			return 1
		}
	}
	if total.Percent() < *minPct {
		fmt.Fprintf(stderr, "cobertura total %.1f %% abaixo do mínimo de %.0f %%\n", total.Percent(), *minPct)
		return 1
	}
	return 0
}
