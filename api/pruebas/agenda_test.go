package pruebas

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestHorarios(t *testing.T) {
	e := NuevoEscenario(t)
	tokAdmin := Token(t, usuarioAdmin)
	tokRecep := Token(t, usuarioRecepcion)
	martes := e.Lunes.AddDate(0, 0, 1)
	fecha := martes.Format("2006-01-02")
	disp := func() dispJSON {
		r := Pedir(t, http.MethodGet, "/disponibilidad?profesional_id="+e.Prof.String()+"&servicio_id="+e.Serv30.String()+"&fecha="+fecha, tokRecep, nil)
		require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
		var d dispJSON
		r.JSON(t, &d)
		return d
	}
	require.Empty(t, disp().Espacios, "el escenario no atiende martes")

	ruta := "/profesionales/" + e.Prof.String() + "/horarios"
	r := Pedir(t, http.MethodPost, ruta, tokAdmin, map[string]any{"dia_semana": 2, "hora_inicio": "08:00", "hora_fin": "10:00"})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var h struct {
		ID uuid.UUID `json:"id"`
	}
	r.JSON(t, &h)
	require.Len(t, disp().Espacios, 4, "08:00 a 10:00 en espacios de 30 min")

	r = Pedir(t, http.MethodPost, ruta, tokAdmin, map[string]any{"dia_semana": 2, "hora_inicio": "09:30", "hora_fin": "11:00"})
	require.Equal(t, http.StatusConflict, r.Status)
	require.Equal(t, "HORARIO_SOLAPADO", r.Codigo())
	r = Pedir(t, http.MethodPost, ruta, tokAdmin, map[string]any{"dia_semana": 2, "hora_inicio": "12:00", "hora_fin": "11:00"})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	r = Pedir(t, http.MethodPost, ruta, tokAdmin, map[string]any{"dia_semana": 9, "hora_inicio": "08:00", "hora_fin": "09:00"})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)

	// Una cita el martes amarra el bloque.
	inicio := time.Date(martes.Year(), martes.Month(), martes.Day(), 8, 30, 0, 0, zonaCR)
	r = e.Reservar(t, tokRecep, e.Pacientes[0], e.Prof, e.Serv30, inicio)
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c citaJSON
	r.JSON(t, &c)

	r = Pedir(t, http.MethodDelete, "/horarios/"+h.ID.String(), tokAdmin, nil)
	require.Equal(t, http.StatusConflict, r.Status)
	require.Equal(t, "HORARIO_CON_CITAS", r.Codigo())

	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/cancelar", tokRecep, map[string]any{"motivo": "prueba"})
	require.Equal(t, http.StatusOK, r.Status)
	r = Pedir(t, http.MethodDelete, "/horarios/"+h.ID.String(), tokAdmin, nil)
	require.Equal(t, http.StatusNoContent, r.Status, string(r.Cuerpo))
	require.Empty(t, disp().Espacios, "sin el bloque no hay espacios")
	r = Pedir(t, http.MethodDelete, "/horarios/"+h.ID.String(), tokAdmin, nil)
	require.Equal(t, http.StatusNotFound, r.Status)
}

func TestBloqueoYConflictos(t *testing.T) {
	e := NuevoEscenario(t)
	tokAdmin := Token(t, usuarioAdmin)
	tokRecep := Token(t, usuarioRecepcion)

	r := e.Reservar(t, tokRecep, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 9, 0))
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c citaJSON
	r.JSON(t, &c)
	antes := consultar(t, tokRecep, e, 0)
	require.True(t, contiene(antes, e.Hora(0, 10, 0)))

	r = Pedir(t, http.MethodPost, "/profesionales/"+e.Prof.String()+"/bloqueos", tokAdmin, map[string]any{
		"inicio": e.Hora(0, 8, 0).Format(time.RFC3339), "fin": e.Hora(0, 12, 0).Format(time.RFC3339), "motivo": "Congreso"})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var b struct {
		Bloqueo struct {
			ID uuid.UUID `json:"id"`
		} `json:"bloqueo"`
		Conflictos []struct {
			ID     uuid.UUID `json:"id"`
			Estado string    `json:"estado"`
		} `json:"conflictos"`
	}
	r.JSON(t, &b)
	require.Len(t, b.Conflictos, 1)
	require.Equal(t, c.ID, b.Conflictos[0].ID)
	require.Equal(t, "reservada", b.Conflictos[0].Estado, "el bloqueo no cancela por su cuenta")

	despues := consultar(t, tokRecep, e, 0)
	require.False(t, contiene(despues, e.Hora(0, 10, 0)), "la mañana ya no se ofrece")
	require.True(t, contiene(despues, e.Hora(0, 13, 0)), "la tarde sigue")

	r = Pedir(t, http.MethodGet, "/profesionales/"+e.Prof.String()+"/conflictos", tokRecep, nil)
	require.Equal(t, []uuid.UUID{c.ID}, IDs(t, r))

	r = e.Reservar(t, tokRecep, e.Pacientes[1], e.Prof, e.Serv30, e.Hora(0, 10, 0))
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	require.Equal(t, "EN_BLOQUEO", r.Codigo())

	// Recepción reprograma la cita en conflicto: deja de aparecer.
	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/reprogramar", tokRecep, map[string]any{"inicio": e.Hora(0, 14, 0).Format(time.RFC3339)})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	require.Empty(t, IDs(t, Pedir(t, http.MethodGet, "/profesionales/"+e.Prof.String()+"/conflictos", tokRecep, nil)))

	r = Pedir(t, http.MethodGet, "/profesionales/"+e.Prof.String()+"/bloqueos?desde="+e.Fecha(0)+"&hasta="+e.Fecha(0), tokAdmin, nil)
	require.Equal(t, []uuid.UUID{b.Bloqueo.ID}, IDs(t, r))
	r = Pedir(t, http.MethodDelete, "/bloqueos/"+b.Bloqueo.ID.String(), tokAdmin, nil)
	require.Equal(t, http.StatusNoContent, r.Status)
	require.True(t, contiene(consultar(t, tokRecep, e, 0), e.Hora(0, 10, 0)), "sin bloqueo vuelve la mañana")

	// Validaciones de entrada.
	r = Pedir(t, http.MethodPost, "/profesionales/"+e.Prof.String()+"/bloqueos", tokAdmin, map[string]any{
		"inicio": e.Hora(0, 12, 0).Format(time.RFC3339), "fin": e.Hora(0, 8, 0).Format(time.RFC3339), "motivo": "x"})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	r = Pedir(t, http.MethodPost, "/profesionales/"+e.Prof.String()+"/bloqueos", tokAdmin, map[string]any{
		"inicio": e.Hora(0, 8, 0).Format(time.RFC3339), "fin": e.Hora(0, 9, 0).Format(time.RFC3339)})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, "sin motivo")
}

func TestProfesionalSoloLoPropio(t *testing.T) {
	e := NuevoEscenario(t)
	p := NuevoProfConUsuario(t)
	tokProf := Token(t, p.Username)
	ajeno := "/profesionales/" + e.Prof.String()
	propio := "/profesionales/" + p.Prof.String()

	require.Len(t, IDs(t, Pedir(t, http.MethodGet, propio+"/horarios", tokProf, nil)), 7)
	r := Pedir(t, http.MethodGet, ajeno+"/horarios", tokProf, nil)
	require.Equal(t, http.StatusForbidden, r.Status)
	r = Pedir(t, http.MethodPost, ajeno+"/horarios", tokProf, map[string]any{"dia_semana": 3, "hora_inicio": "08:00", "hora_fin": "09:00"})
	require.Equal(t, http.StatusForbidden, r.Status)
	r = Pedir(t, http.MethodPost, ajeno+"/bloqueos", tokProf, map[string]any{
		"inicio": e.Hora(0, 8, 0).Format(time.RFC3339), "fin": e.Hora(0, 9, 0).Format(time.RFC3339), "motivo": "x"})
	require.Equal(t, http.StatusForbidden, r.Status)

	// Borrar el horario de otro profesional: no lo ve, 404.
	horariosAjenos := IDs(t, Pedir(t, http.MethodGet, ajeno+"/horarios", Token(t, usuarioAdmin), nil))
	require.NotEmpty(t, horariosAjenos)
	r = Pedir(t, http.MethodDelete, "/horarios/"+horariosAjenos[0].String(), tokProf, nil)
	require.Equal(t, http.StatusNotFound, r.Status)

	// El suyo sí lo puede borrar.
	r = Pedir(t, http.MethodPost, propio+"/bloqueos", tokProf, map[string]any{
		"inicio": e.Hora(0, 8, 0).Format(time.RFC3339), "fin": e.Hora(0, 9, 0).Format(time.RFC3339), "motivo": "Cita médica"})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))

	// Recepción no gestiona horarios ni bloqueos.
	r = Pedir(t, http.MethodGet, ajeno+"/horarios", Token(t, usuarioRecepcion), nil)
	require.Equal(t, http.StatusForbidden, r.Status)
	r = Pedir(t, http.MethodPost, ajeno+"/bloqueos", Token(t, usuarioRecepcion), map[string]any{
		"inicio": e.Hora(0, 8, 0).Format(time.RFC3339), "fin": e.Hora(0, 9, 0).Format(time.RFC3339), "motivo": "x"})
	require.Equal(t, http.StatusForbidden, r.Status)
}

func TestAgendaDelDia(t *testing.T) {
	e := NuevoEscenario(t)
	tokRecep := Token(t, usuarioRecepcion)
	var ids []uuid.UUID
	for i, h := range []int{11, 9} {
		r := e.Reservar(t, tokRecep, e.Pacientes[i], e.Prof, e.Serv30, e.Hora(0, h, 0))
		require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
		var c citaJSON
		r.JSON(t, &c)
		ids = append(ids, c.ID)
	}

	r := Pedir(t, http.MethodGet, "/agenda?fecha="+e.Fecha(0)+"&profesional_id="+e.Prof.String(), tokRecep, nil)
	require.Equal(t, []uuid.UUID{ids[1], ids[0]}, IDs(t, r), "ordenadas por hora")
	todas := IDs(t, Pedir(t, http.MethodGet, "/agenda?fecha="+e.Fecha(0), tokRecep, nil))
	require.Subset(t, todas, ids)

	// El profesional: la suya por omisión, la de otro no.
	p := NuevoProfConUsuario(t)
	tokProf := Token(t, p.Username)
	require.Empty(t, IDs(t, Pedir(t, http.MethodGet, "/agenda?fecha="+e.Fecha(0), tokProf, nil)))
	r = Pedir(t, http.MethodGet, "/agenda?fecha="+e.Fecha(0)+"&profesional_id="+e.Prof.String(), tokProf, nil)
	require.Equal(t, http.StatusForbidden, r.Status)

	r = Pedir(t, http.MethodGet, "/agenda?fecha=14-11-2026", tokRecep, nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	r = Pedir(t, http.MethodGet, "/agenda", tokRecep, nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
}
