package utils

import "testing"

func TestParseQuantity(t *testing.T) {
	cases := []struct {
		text    string
		want    float64
		wantErr bool
	}{
		{text: "2,5", want: 2.5},
		{text: "2.5", want: 2.5},
		{text: " 10 ", want: 10},
		{text: "1.250,75", want: 1250.75},
		{text: "0", want: 0},
		{text: "-3", want: -3},
		{text: "0,1234", want: 0.123},
		{text: "", wantErr: true},
		{text: "abc", wantErr: true},
		{text: "1e3", wantErr: true},
		{text: "Inf", wantErr: true},
		{text: "2,5,1", wantErr: true},
	}

	for _, c := range cases {
		got, err := ParseQuantity(c.text)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseQuantity(%q) = %v, esperava erro", c.text, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseQuantity(%q) deu erro inesperado: %v", c.text, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseQuantity(%q) = %v, esperado %v", c.text, got, c.want)
		}
	}
}

func TestFormatQuantity(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{value: 0, want: "0"},
		{value: 2.5, want: "2,5"},
		{value: 999, want: "999"},
		{value: 1200, want: "1.200"},
		{value: 1234567.125, want: "1.234.567,125"},
		{value: 0.1 + 0.2, want: "0,3"},
		{value: -4.5, want: "-4,5"},
		{value: -0.0001, want: "0"},
	}

	for _, c := range cases {
		if got := FormatQuantity(c.value); got != c.want {
			t.Errorf("FormatQuantity(%v) = %q, esperado %q", c.value, got, c.want)
		}
	}
}

func TestValidateUnit(t *testing.T) {
	for _, unit := range []string{"saco", "m³", "un", "milheiro"} {
		if !ValidateUnit(unit) {
			t.Errorf("ValidateUnit(%q) = false, esperado true", unit)
		}
	}
	for _, unit := range []string{"", "sacos", "SACO", "m3"} {
		if ValidateUnit(unit) {
			t.Errorf("ValidateUnit(%q) = true, esperado false", unit)
		}
	}
}

func TestValidateDate(t *testing.T) {
	if !ValidateDate("2026-09-15") {
		t.Error("ValidateDate(2026-09-15) deveria ser válida")
	}
	for _, text := range []string{"", "15/09/2026", "2026-13-01", "2026-02-30"} {
		if ValidateDate(text) {
			t.Errorf("ValidateDate(%q) deveria ser inválida", text)
		}
	}
}
