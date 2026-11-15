package http

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Pruebas de las funciones puras de ayudas.go (sin base ni red).

func TestInstante(t *testing.T) {
	cr, _ := time.LoadLocation("America/Costa_Rica")
	casos := []struct {
		valor    string
		finDeDia bool
		quiere   string // RFC 3339 en UTC, "" = nil, "error" = error
	}{
		{"", false, ""},
		{"2026-11-20", false, "2026-11-20T06:00:00Z"},               // medianoche en Costa Rica (UTC-6)
		{"2026-11-20", true, "2026-11-21T06:00:00Z"},                // "hasta" incluye todo el día
		{"2026-11-20T10:00:00-06:00", true, "2026-11-20T16:00:00Z"}, // RFC 3339 va tal cual
		{"20/11/2026", false, "error"},
		{"ayer", false, "error"},
	}
	for _, c := range casos {
		got, err := instante(c.valor, cr, c.finDeDia)
		switch {
		case c.quiere == "error":
			if err == nil {
				t.Errorf("instante(%q): esperaba error", c.valor)
			}
		case c.quiere == "":
			if err != nil || got != nil {
				t.Errorf("instante(%q) = %v, %v; esperaba nil", c.valor, got, err)
			}
		default:
			if err != nil || got.UTC().Format(time.RFC3339) != c.quiere {
				t.Errorf("instante(%q, %v) = %v, %v; esperaba %s", c.valor, c.finDeDia, got, err, c.quiere)
			}
		}
	}
}

func TestPaginacion(t *testing.T) {
	casos := []struct {
		query        string
		limite, desp int
		esperaError  bool
	}{
		{"", 50, 0, false},
		{"?limite=10&desplazamiento=20", 10, 20, false},
		{"?limite=0", 0, 0, true},
		{"?limite=201", 0, 0, true},
		{"?limite=abc", 0, 0, true},
		{"?desplazamiento=-1", 0, 0, true},
	}
	for _, c := range casos {
		l, d, err := paginacion(httptest.NewRequest("GET", "/x"+c.query, nil))
		if (err != nil) != c.esperaError || (!c.esperaError && (l != c.limite || d != c.desp)) {
			t.Errorf("paginacion(%q) = %d, %d, %v", c.query, l, d, err)
		}
	}
}

func TestRequerido(t *testing.T) {
	if err := requerido(map[string]string{"a": "x", "b": "y"}); err != nil {
		t.Errorf("todo presente: %v", err)
	}
	err := requerido(map[string]string{"nombre": " ", "apellidos": "", "correo": "x"})
	if err == nil || err.Error() != "SOLICITUD_INVALIDA: Faltan campos obligatorios: apellidos, nombre" {
		t.Errorf("faltantes en orden: %v", err)
	}
}
