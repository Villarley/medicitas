package pruebas

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestErroresNoTumbanLaAPI: entradas malas reciben su código y el servicio sigue vivo.
//
// Cubre: requisitos de operación 2 y 3
func TestErroresNoTumbanLaAPI(t *testing.T) {
	tok := Token(t, usuarioRecepcion)
	casos := []struct {
		metodo, ruta string
		cuerpo       any
		status       int
	}{
		{http.MethodPost, "/citas", `{"paciente_id": `, http.StatusUnprocessableEntity},
		{http.MethodPost, "/citas", "", http.StatusUnprocessableEntity},
		{http.MethodPost, "/citas", map[string]any{"paciente_id": "no-es-uuid"}, http.StatusUnprocessableEntity},
		{http.MethodPost, "/citas", map[string]any{"inicio": "mañana"}, http.StatusUnprocessableEntity},
		{http.MethodPost, "/citas", map[string]any{}, http.StatusUnprocessableEntity},
		{http.MethodGet, "/citas/no-es-uuid", nil, http.StatusUnprocessableEntity},
		{http.MethodGet, "/citas/" + uuid.NewString(), nil, http.StatusNotFound},
		{http.MethodPost, "/citas/" + uuid.NewString() + "/cancelar", map[string]any{"motivo": "x"}, http.StatusNotFound},
		{http.MethodGet, "/disponibilidad?profesional_id=x", nil, http.StatusUnprocessableEntity},
		{http.MethodGet, "/disponibilidad?profesional_id=" + uuid.NewString() + "&servicio_id=" + uuid.NewString() + "&fecha=31-12-2030", nil, http.StatusUnprocessableEntity},
		{http.MethodGet, "/disponibilidad?profesional_id=" + uuid.NewString() + "&servicio_id=" + uuid.NewString() + "&fecha=2030-12-31", nil, http.StatusNotFound},
		{http.MethodGet, "/ruta-que-no-existe", nil, http.StatusNotFound},
		{http.MethodDelete, "/citas", nil, http.StatusMethodNotAllowed},
	}
	for _, c := range casos {
		r := Pedir(t, c.metodo, c.ruta, tok, c.cuerpo)
		require.Equal(t, c.status, r.Status, "%s %s -> %s", c.metodo, c.ruta, r.Cuerpo)
		require.NotEmpty(t, r.Codigo(), "todo error trae codigo: %s", r.Cuerpo)
	}
	r := Pedir(t, http.MethodGet, "/salud", "", nil)
	require.Equal(t, http.StatusOK, r.Status)
}
