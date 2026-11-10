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

// TestCicloDeLaCita recorre la vida de una cita y cada transición prohibida.
//
// Cubre: RF9, RF10, RF11, RF12, RF13, criterio de aceptación 2
func TestCicloDeLaCita(t *testing.T) {
	e := NuevoEscenario(t)
	tok := Token(t, usuarioRecepcion)

	// Reservar: 201
	r := e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 9, 0))
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c citaJSON
	r.JSON(t, &c)
	require.Equal(t, "reservada", c.Estado)

	// Desenlace antes de la hora: 422 DESENLACE_PREMATURO
	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/desenlace", tok, map[string]any{"desenlace": "atendida"})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	require.Equal(t, "DESENLACE_PREMATURO", r.Codigo())

	// Reprogramar: 201 con cita nueva; la vieja queda 'reprogramada'
	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/reprogramar", tok, map[string]any{"inicio": e.Hora(0, 10, 0).Format(time.RFC3339)})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var nueva citaJSON
	r.JSON(t, &nueva)
	require.Equal(t, c.ID, nueva.CitaOrigenID.UUID)
	r = Pedir(t, http.MethodGet, "/citas/"+c.ID.String(), tok, nil)
	var vieja citaJSON
	r.JSON(t, &vieja)
	require.Equal(t, "reprogramada", vieja.Estado)

	// Reprogramar otra vez la vieja: 409
	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/reprogramar", tok, map[string]any{"inicio": e.Hora(0, 11, 0).Format(time.RFC3339)})
	require.Equal(t, http.StatusConflict, r.Status)
	require.Equal(t, "TRANSICION_INVALIDA", r.Codigo())

	// Cancelar sin motivo: 422; con motivo: 200
	r = Pedir(t, http.MethodPost, "/citas/"+nueva.ID.String()+"/cancelar", tok, map[string]any{"motivo": "  "})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	r = Pedir(t, http.MethodPost, "/citas/"+nueva.ID.String()+"/cancelar", tok, map[string]any{"motivo": "El paciente llamó para cancelar"})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))

	// Desde un estado final no se sale: 409
	r = Pedir(t, http.MethodPost, "/citas/"+nueva.ID.String()+"/cancelar", tok, map[string]any{"motivo": "otra vez"})
	require.Equal(t, http.StatusConflict, r.Status)
	require.Equal(t, "TRANSICION_INVALIDA", r.Codigo())
	r = Pedir(t, http.MethodPost, "/citas/"+nueva.ID.String()+"/desenlace", tok, map[string]any{"desenlace": "ausente"})
	require.Equal(t, http.StatusConflict, r.Status)

	// Desenlace con valor raro: 422
	r = Pedir(t, http.MethodPost, "/citas/"+nueva.ID.String()+"/desenlace", tok, map[string]any{"desenlace": "cancelada"})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)

	// Cita en el pasado: el desenlace sí se registra (200)
	pasada := crearCitaPasada(t, e)
	r = Pedir(t, http.MethodPost, "/citas/"+pasada.String()+"/desenlace", tok, map[string]any{"desenlace": "atendida"})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	var cerrada citaJSON
	r.JSON(t, &cerrada)
	require.Equal(t, "atendida", cerrada.Estado)

	// Reservar en el pasado por la API: 422
	r = e.Reservar(t, tok, e.Pacientes[1], e.Prof, e.Serv30, time.Now().Add(-time.Hour))
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
}

// crearCitaPasada inserta directo en la base una cita reservada el lunes pasado
// (la API no deja reservar en el pasado, pero la semilla y la vida real sí las tienen).
func crearCitaPasada(t *testing.T, e *Escenario) uuid.UUID {
	t.Helper()
	hoy := time.Now().In(zonaCR)
	lunes := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 9, 0, 0, 0, zonaCR).AddDate(0, 0, -7)
	for lunes.Weekday() != time.Monday {
		lunes = lunes.AddDate(0, 0, -1)
	}
	var id uuid.UUID
	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO cita (clinica_id, paciente_id, profesional_id, servicio_id, inicio, fin, creada_por)
			 VALUES (app_clinica_actual(), $1, $2, $3, $4, $4, app_usuario_actual()) RETURNING id`,
			e.Pacientes[3], e.Prof, e.Serv30, lunes).Scan(&id)
	})
	return id
}
