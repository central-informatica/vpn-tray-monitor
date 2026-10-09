package main

import (
	"strings"
	"testing"
)

func TestFromTag(t *testing.T) {
	cases := []struct {
		tag, semver, product string
		pre                  bool
	}{
		{"v0.0.1", "0.0.1", "0.0.199", false},
		{"v0.0.2", "0.0.2", "0.0.299", false},
		{"v2.1.0-rc.1", "2.1.0-rc.1", "2.1.1", true},
		{"v2.1.0-rc.98", "2.1.0-rc.98", "2.1.98", true},
		{"v2.1.0", "2.1.0", "2.1.99", false},
		{"v2.1.3", "2.1.3", "2.1.399", false},
		{"v255.255.654", "255.255.654", "255.255.65499", false},
		{"v255.255.654-rc.98", "255.255.654-rc.98", "255.255.65498", true},
	}
	for _, c := range cases {
		v, err := FromTag(c.tag)
		if err != nil {
			t.Fatalf("%s: %v", c.tag, err)
		}
		if v.Tag != c.tag || v.Semver != c.semver || v.Product != c.product || v.FileVer != c.product+".0" || v.Prerelease != c.pre {
			t.Errorf("%s: %+v", c.tag, v)
		}
	}
}

// Uma rc é sempre menor que a final da mesma versão, e a final menor que
// a rc da versão seguinte: é o que o MajorUpgrade (3 campos) compara.
func TestFromTagOrdersForMajorUpgrade(t *testing.T) {
	seq := []string{"v1.0.0-rc.1", "v1.0.0-rc.2", "v1.0.0", "v1.0.1-rc.1", "v1.0.1", "v1.1.0-rc.1", "v1.1.0", "v2.0.0"}
	var prev [3]int
	for i, tag := range seq {
		v, err := FromTag(tag)
		if err != nil {
			t.Fatal(err)
		}
		var cur [3]int
		for j, p := range strings.Split(v.Product, ".") {
			for _, r := range p {
				cur[j] = cur[j]*10 + int(r-'0')
			}
		}
		if i > 0 && !(cur[0] > prev[0] || cur[0] == prev[0] && (cur[1] > prev[1] || cur[1] == prev[1] && cur[2] > prev[2])) {
			t.Errorf("%s (%s) não é maior que o anterior %v", tag, v.Product, prev)
		}
		prev = cur
	}
}

func TestFromTagRejects(t *testing.T) {
	for _, tag := range []string{
		"", "2.1.0", "v2.1", "v2.1.0.0", "v02.1.0", "v2.01.0", "v2.1.00",
		"v2.1.0-rc.0", "v2.1.0-rc.99", "v2.1.0-rc.01", "v2.1.0-beta.1", "v2.1.0-rc1", "v2.1.0+build",
		"v256.0.0", "v0.256.0", "v0.0.655", "v99999999999999999999.0.0", " v2.1.0", "v2.1.0\n",
	} {
		if v, err := FromTag(tag); err == nil {
			t.Errorf("%q aceita: %+v", tag, v)
		}
	}
}
