package main

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version é a tag de release convertida para cada uso (§10.2).
type Version struct {
	Tag        string // a tag como veio: v2.1.0-rc.1
	Semver     string // sem o "v": 2.1.0-rc.1 (nome dos arquivos, recursos do exe)
	Product    string // ProductVersion do MSI: X.Y.(Z×100+N) ou X.Y.(Z×100+99)
	FileVer    string // versão numérica dos recursos do exe: Product + ".0"
	Prerelease bool   // tag -rc.N
}

// Limites da §10.2: o MSI guarda maior e menor em 8 bits e o build em 16
// (≤ 65535); Z×100+99 ≤ 65535 dá Z ≤ 654, e N vai de 1 a 98 (99 é a final).
const (
	maxMajor = 255
	maxMinor = 255
	maxPatch = 654
	maxRC    = 98
)

// Só vX.Y.Z e vX.Y.Z-rc.N, sem zeros à esquerda; qualquer outro sufixo
// (-beta, +build) é recusado para a conversão nunca ser ambígua.
var tagRe = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.([1-9][0-9]*))?$`)

// FromTag converte a tag de release; erro se fora do formato ou dos limites.
func FromTag(tag string) (Version, error) {
	m := tagRe.FindStringSubmatch(tag)
	if m == nil {
		return Version{}, fmt.Errorf("tag %q fora do formato vX.Y.Z ou vX.Y.Z-rc.N", tag)
	}
	num := func(s string, max int, name string) (int, error) {
		n, err := strconv.Atoi(s)
		if err != nil || n > max {
			return 0, fmt.Errorf("tag %q: %s %s acima do limite %d", tag, name, s, max)
		}
		return n, nil
	}
	x, err := num(m[1], maxMajor, "X")
	if err != nil {
		return Version{}, err
	}
	y, err := num(m[2], maxMinor, "Y")
	if err != nil {
		return Version{}, err
	}
	z, err := num(m[3], maxPatch, "Z")
	if err != nil {
		return Version{}, err
	}
	build, pre := z*100+99, false
	if m[4] != "" {
		n, err := num(m[4], maxRC, "N")
		if err != nil {
			return Version{}, err
		}
		build, pre = z*100+n, true
	}
	product := fmt.Sprintf("%d.%d.%d", x, y, build)
	return Version{Tag: tag, Semver: tag[1:], Product: product, FileVer: product + ".0", Prerelease: pre}, nil
}
