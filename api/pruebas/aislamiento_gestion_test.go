package pruebas

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type idsSanPedro struct {
	clinica, paciente, profesional, servicio, especialidad, horario, usuario uuid.UUID
	identificacion                                                           string
}

func leerSanPedro(t *testing.T) idsSanPedro {
	t.Helper()
	var d idsSanPedro
	ComoAdminDe(t, "admin.san-pedro", func(q consulta) {
		q(`SELECT id, clinica_id, identificacion FROM paciente ORDER BY id LIMIT 1`, &d.paciente, &d.clinica, &d.identificacion)
		q(`SELECT id, especialidad_id FROM profesional ORDER BY id LIMIT 1`, &d.profesional, &d.especialidad)
		q(`SELECT id FROM servicio ORDER BY id LIMIT 1`, &d.servicio)
		q(`SELECT id FROM horario_atencion ORDER BY id LIMIT 1`, &d.horario)
		q(`SELECT id FROM usuario WHERE rol_codigo = 'recepcion' ORDER BY id LIMIT 1`, &d.usuario)
	})
	return d
}

func TestFugaEnRutasDeGestion(t *testing.T) {
	sp := leerSanPedro(t)
	tokAdmin := Token(t, usuarioAdmin)
	tokRecep := Token(t, usuarioRecepcion)
	tokProf := Token(t, usuarioProf1)
	manip := "?clinica_id=" + sp.clinica.String()

	// Lectura directa por id: 404, como si no existiera.
	for _, intento := range []struct {
		tok, ruta string
	}{
		{tokRecep, "/pacientes/" + sp.paciente.String()},
		{tokRecep, "/pacientes/" + sp.paciente.String() + manip},
		{tokRecep, "/pacientes/" + sp.paciente.String() + "/historial"},
		{tokProf, "/pacientes/" + sp.paciente.String()},
		{tokRecep, "/profesionales/" + sp.profesional.String()},
		{tokAdmin, "/profesionales/" + sp.profesional.String() + "/horarios"},
		{tokAdmin, "/profesionales/" + sp.profesional.String() + "/bloqueos"},
		{tokRecep, "/profesionales/" + sp.profesional.String() + "/conflictos"},
		{tokRecep, "/agenda?fecha=2030-01-07&profesional_id=" + sp.profesional.String()},
	} {
		r := Pedir(t, http.MethodGet, intento.ruta, intento.tok, nil)
		require.Equal(t, http.StatusNotFound, r.Status, "GET %s: %s", intento.ruta, r.Cuerpo)
	}

	// Escritura sobre recursos ajenos: 404 y nada cambia.
	for _, intento := range []struct {
		tok, metodo, ruta string
		cuerpo            any
	}{
		{tokRecep, http.MethodPatch, "/pacientes/" + sp.paciente.String(), map[string]any{"telefono": "0000-0000"}},
		{tokAdmin, http.MethodPatch, "/servicios/" + sp.servicio.String(), map[string]any{"activo": false}},
		{tokAdmin, http.MethodPut, "/profesionales/" + sp.profesional.String() + "/servicios", map[string]any{"servicio_ids": []uuid.UUID{}}},
		{tokAdmin, http.MethodPost, "/profesionales/" + sp.profesional.String() + "/horarios", map[string]any{"dia_semana": 6, "hora_inicio": "01:00", "hora_fin": "02:00"}},
		{tokAdmin, http.MethodPost, "/profesionales/" + sp.profesional.String() + "/bloqueos", map[string]any{
			"inicio": time.Now().Add(24 * time.Hour).Format(time.RFC3339), "fin": time.Now().Add(48 * time.Hour).Format(time.RFC3339), "motivo": "fuga"}},
		{tokAdmin, http.MethodDelete, "/horarios/" + sp.horario.String(), nil},
		{tokAdmin, http.MethodPatch, "/usuarios/" + sp.usuario.String(), map[string]any{"rol": "profesional"}},
		{tokAdmin, http.MethodDelete, "/usuarios/" + sp.usuario.String(), nil},
		{tokProf, http.MethodGet, "/citas/" + uuid.NewString() + "/nota", nil},
		// Referenciar algo ajeno al crear algo propio.
		{tokAdmin, http.MethodPost, "/servicios", map[string]any{"nombre": "Fuga " + uuid.NewString()[:6], "duracion_min": 30, "especialidad_id": sp.especialidad}},
		{tokAdmin, http.MethodPost, "/profesionales", map[string]any{"nombre": "Fuga", "especialidad_id": sp.especialidad}},
		{tokAdmin, http.MethodPost, "/profesionales", map[string]any{"nombre": "Fuga", "especialidad_id": MiEspecialidad(t), "usuario_id": sp.usuario}},
	} {
		r := Pedir(t, intento.metodo, intento.ruta, intento.tok, intento.cuerpo)
		require.Equal(t, http.StatusNotFound, r.Status, "%s %s: %s", intento.metodo, intento.ruta, r.Cuerpo)
	}
	// El servicio ajeno sigue activo y su horario sigue ahí.
	var activo, horario bool
	ComoAdmin(t, "admin.san-pedro", func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT activo FROM servicio WHERE id = $1`, sp.servicio).Scan(&activo); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM horario_atencion WHERE id = $1)`, sp.horario).Scan(&horario)
	})
	require.True(t, activo, "el servicio ajeno sigue activo")
	require.True(t, horario, "el horario ajeno sigue ahí")

	// Listados: nada de San Pedro, ni siquiera mandando su clinica_id.
	for _, intento := range []struct {
		tok, ruta string
		ajeno     uuid.UUID
	}{
		{tokRecep, "/pacientes?limite=200&buscar=" + sp.identificacion + "&clinica_id=" + sp.clinica.String(), sp.paciente},
		{tokRecep, "/servicios" + manip, sp.servicio},
		{tokRecep, "/especialidades" + manip, sp.especialidad},
		{tokRecep, "/profesionales" + manip, sp.profesional},
		{tokAdmin, "/usuarios" + manip, sp.usuario},
	} {
		require.NotContains(t, IDs(t, Pedir(t, http.MethodGet, intento.ruta, intento.tok, nil)), intento.ajeno, intento.ruta)
	}
	require.Empty(t, IDs(t, Pedir(t, http.MethodGet, "/pacientes?buscar="+sp.identificacion, tokRecep, nil)))
	require.Empty(t, Bitacora(t, "paciente_id="+sp.paciente.String()), "la bitácora ajena no se ve")

	// La agenda y los reportes de Central no incluyen citas de San Pedro.
	ajena := datosDeSanPedro(t, Token(t, "recepcion.san-pedro"))
	fecha := ajena.inicio.In(zonaCR).Format("2006-01-02")
	require.NotContains(t, IDs(t, Pedir(t, http.MethodGet, "/agenda?fecha="+fecha, tokRecep, nil)), ajena.cita)
	r := Pedir(t, http.MethodGet, "/citas/"+ajena.cita.String()+"/nota", tokProf, nil)
	require.Equal(t, http.StatusNotFound, r.Status)
}

func MiEspecialidad(t *testing.T) uuid.UUID {
	t.Helper()
	ids := IDs(t, Pedir(t, http.MethodGet, "/especialidades", Token(t, usuarioAdmin), nil))
	require.NotEmpty(t, ids)
	return ids[0]
}
