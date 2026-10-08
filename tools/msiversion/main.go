// Comando msiversion converte a tag de release nas versões que o build usa
// (§10.2): a semver completa (nome dos arquivos, recursos do exe, `version`),
// a ProductVersion do MSI e a versão numérica dos recursos do exe.
//
// Uso:
//
//	go run ./tools/msiversion v2.1.0-rc.1            # chave=valor, para $GITHUB_OUTPUT
//	go run ./tools/msiversion -field product v2.1.0  # só um campo (Makefile)
//	go run ./tools/msiversion -dev "$(git describe)" # fora de tag: build de desenvolvimento
//
// Com -dev, uma entrada que não é tag de release vira versão de
// desenvolvimento (product 0.0.0) em vez de erro; uma tag válida segue a
// conversão normal.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// devVersion é a versão de um build fora de tag: a ProductVersion 0.0.0 é
// menor que qualquer release, então um MSI de desenvolvimento nunca impede
// instalar uma release por cima.
func devVersion(describe string) Version {
	s := strings.TrimPrefix(strings.TrimSpace(describe), "v")
	if s == "" {
		s = "dev"
	}
	return Version{Tag: describe, Semver: s, Product: "0.0.0", FileVer: "0.0.0.0", Prerelease: true}
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("msiversion", flag.ContinueOnError)
	fs.SetOutput(stderr)
	field := fs.String("field", "", "imprime só este campo (tag, semver, product, filever, prerelease)")
	dev := fs.Bool("dev", false, "fora de tag, devolve versão de desenvolvimento em vez de erro")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "uso: msiversion [-dev] [-field nome] <tag>")
		return 2
	}
	v, err := FromTag(fs.Arg(0))
	if err != nil {
		if !*dev {
			fmt.Fprintln(stderr, "erro:", err)
			return 1
		}
		v = devVersion(fs.Arg(0))
	}
	fields := []struct{ k, v string }{
		{"tag", v.Tag}, {"semver", v.Semver}, {"product", v.Product},
		{"filever", v.FileVer}, {"prerelease", fmt.Sprint(v.Prerelease)},
	}
	for _, f := range fields {
		switch {
		case *field == "":
			fmt.Fprintf(stdout, "%s=%s\n", f.k, f.v)
		case *field == f.k:
			fmt.Fprintln(stdout, f.v)
			return 0
		}
	}
	if *field != "" {
		fmt.Fprintf(stderr, "campo desconhecido %q\n", *field)
		return 2
	}
	return 0
}
