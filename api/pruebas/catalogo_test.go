package pruebas

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCatalogo(t *testing.T) {
	tokAdmin := Token(t, usuarioAdmin)
	tokRecep := Token(t, usuarioRecepcion)
	sufijo := uuid.NewString()[:8]

	crear := func(ruta string, cuerpo map[string]any) uuid.UUID {
		t.Helper()
		r := Pedir(t, http.MethodPost, ruta, tokAdmin, cuerpo)
		require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
		var x struct {
			ID uuid.UUID `json:"id"`
		}
		r.JSON(t, &x)
		return x.ID
	}

	espA := crear("/especialidades", map[string]any{"nombre": "Cardiología " + sufijo})
	espB := crear("/especialidades", map[string]any{"nombre": "Dermatología " + sufijo})
	r := Pedir(t, http.MethodPost, "/especialidades", tokAdmin, map[string]any{"nombre": "Cardiología " + sufijo})
	require.Equal(t, http.StatusConflict, r.Status, "especialidad repetida")

	srvA := crear("/servicios", map[string]any{"nombre": "Electrocardiograma " + sufijo, "duracion_min": 40, "especialidad_id": espA})
	srvB := crear("/servicios", map[string]any{"nombre": "Revisión de lunares " + sufijo, "duracion_min": 20, "especialidad_id": espB})

	r = Pedir(t, http.MethodPost, "/servicios", tokAdmin, map[string]any{"nombre": "Raro " + sufijo, "duracion_min": 7, "especialidad_id": espA})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, "duración que no es múltiplo de 5")
	r = Pedir(t, http.MethodPost, "/servicios", tokAdmin, map[string]any{"nombre": "Sin esp " + sufijo, "duracion_min": 30, "especialidad_id": uuid.New()})
	require.Equal(t, http.StatusNotFound, r.Status, "especialidad inexistente")

	// La duración no se cambia por PATCH.
	r = Pedir(t, http.MethodPatch, "/servicios/"+srvA.String(), tokAdmin, map[string]any{"duracion_min": 60})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	require.Equal(t, "CAMPO_INMUTABLE", r.Codigo())
	r = Pedir(t, http.MethodPatch, "/servicios/"+srvA.String(), tokAdmin, map[string]any{"nombre": "ECG " + sufijo})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))

	prof := crear("/profesionales", map[string]any{"nombre": "Dr. Corazón " + sufijo, "especialidad_id": espA})

	// Un servicio de otra especialidad no se le asigna.
	r = Pedir(t, http.MethodPut, "/profesionales/"+prof.String()+"/servicios", tokAdmin, map[string]any{"servicio_ids": []uuid.UUID{srvA, srvB}})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	require.Equal(t, "ESPECIALIDAD_NO_COINCIDE", r.Codigo())

	r = Pedir(t, http.MethodPut, "/profesionales/"+prof.String()+"/servicios", tokAdmin, map[string]any{"servicio_ids": []uuid.UUID{srvA}})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	var p struct {
		ServicioIDs []uuid.UUID `json:"servicio_ids"`
	}
	r.JSON(t, &p)
	require.Equal(t, []uuid.UUID{srvA}, p.ServicioIDs)

	// Filtrar profesionales por servicio (lo usa recepción para reservar).
	require.Equal(t, []uuid.UUID{prof}, IDs(t, Pedir(t, http.MethodGet, "/profesionales?servicio_id="+srvA.String(), tokRecep, nil)))

	// Recepción lee el catálogo pero no lo modifica.
	require.Contains(t, IDs(t, Pedir(t, http.MethodGet, "/servicios", tokRecep, nil)), srvA)
	for _, intento := range []struct{ metodo, ruta string }{
		{http.MethodPost, "/especialidades"}, {http.MethodPost, "/servicios"}, {http.MethodPatch, "/servicios/" + srvA.String()},
		{http.MethodPost, "/profesionales"}, {http.MethodPut, "/profesionales/" + prof.String() + "/servicios"},
	} {
		r = Pedir(t, intento.metodo, intento.ruta, tokRecep, map[string]any{"nombre": "x"})
		require.Equal(t, http.StatusForbidden, r.Status, intento.ruta)
	}
}

func TestDesactivarServicioInvalidaCache(t *testing.T) {
	e := NuevoEscenario(t)
	tokAdmin := Token(t, usuarioAdmin)
	tokRecep := Token(t, usuarioRecepcion)

	consultar(t, tokRecep, e, 0)
	d := consultar(t, tokRecep, e, 0)
	require.Equal(t, "cache", d.Fuente)
	require.NotEmpty(t, d.Espacios)

	r := Pedir(t, http.MethodPatch, "/servicios/"+e.Serv30.String(), tokAdmin, map[string]any{"activo": false})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	d = consultar(t, tokRecep, e, 0)
	require.Empty(t, d.Espacios, "un servicio inactivo no tiene espacios")
	r = e.Reservar(t, tokRecep, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 9, 0))
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, "tampoco se reserva")

	r = Pedir(t, http.MethodPatch, "/servicios/"+e.Serv30.String(), tokAdmin, map[string]any{"activo": true})
	require.Equal(t, http.StatusOK, r.Status)
	require.NotEmpty(t, consultar(t, tokRecep, e, 0).Espacios)
}
