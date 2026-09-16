package main

import "testing"

func TestSafeReturnPath(t *testing.T) {
	cases := map[string]string{
		"/estoque":                     "/estoque",
		"/movimentacoes?tipo=SAIDA":    "/movimentacoes",
		"/obras?editar=3#topo":         "/obras",
		"":                             "/dashboard",
		"estoque":                      "/dashboard",
		"https://site-falso.com/login": "/dashboard",
		"//site-falso.com":             "/dashboard",
		"/\\site-falso.com":            "/dashboard",
		"javascript:alert(1)":          "/dashboard",
		"%zz":                          "/dashboard",
	}
	for input, want := range cases {
		if got := safeReturnPath(input); got != want {
			t.Errorf("safeReturnPath(%q) = %q, esperado %q", input, got, want)
		}
	}
}
