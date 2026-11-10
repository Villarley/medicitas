package pruebas

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func jsonDecode(resp *http.Response, v any) error { return json.NewDecoder(resp.Body).Decode(v) }

// TestHorarioYBloqueos: no se reserva fuera del horario ni dentro de un bloqueo.
//
// Cubre: RF9, I3
func TestHorarioYBloqueos(t *testing.T) {
	e := NuevoEscenario(t)
	tok := Token(t, usuarioRecepcion)

	r := e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 12, 0)) // almuerzo
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	require.Equal(t, "FUERA_DE_HORARIO", r.Codigo())
	r = e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv45, e.Hora(0, 11, 30)) // no cabe: termina 12:15
	require.Equal(t, "FUERA_DE_HORARIO", r.Codigo())
	r = e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 9, 0).AddDate(0, 0, 1)) // martes
	require.Equal(t, "FUERA_DE_HORARIO", r.Codigo())

	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO bloqueo_agenda (clinica_id, profesional_id, inicio, fin, motivo, creado_por)
			 VALUES (app_clinica_actual(), $1, $2, $3, 'Permiso', app_usuario_actual())`,
			e.Prof, e.Hora(0, 13, 0), e.Hora(0, 15, 0))
		return err
	})
	r = e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 14, 0))
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	require.Equal(t, "EN_BLOQUEO", r.Codigo())
	r = e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 15, 0))
	require.Equal(t, http.StatusCreated, r.Status, "justo al terminar el bloqueo sí se puede")
}

// TestDuracionInmutable: la duración la manda el servicio. Campos extra como
// "fin" o "duracion" en el cuerpo se ignoran.
//
// Cubre: RF9, I4
func TestDuracionInmutable(t *testing.T) {
	e := NuevoEscenario(t)
	tok := Token(t, usuarioRecepcion)
	ini := e.Hora(0, 8, 0)
	r := Pedir(t, http.MethodPost, "/citas", tok, map[string]any{
		"paciente_id": e.Pacientes[0], "profesional_id": e.Prof, "servicio_id": e.Serv30,
		"inicio": ini.Format(time.RFC3339), "fin": ini.Add(3 * time.Hour).Format(time.RFC3339), "duracion": 180,
	})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c citaJSON
	r.JSON(t, &c)
	require.Equal(t, 30*time.Minute, c.Fin.Sub(c.Inicio))

	// Ni siquiera directo en la base se puede estirar una cita.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, "SET LOCAL ROLE db_recepcion")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.clinica_id', $1, true)`, c.ClinicaID.String())
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE cita SET fin = fin + interval '1 hour' WHERE id = $1`, c.ID)
	require.ErrorContains(t, err, "no se modifican")
}

// TestReprogramarSolapandoConsigoMisma: mover una cita de 45 min media hora
// más tarde se cruza con su propio espacio viejo; como el viejo se libera en
// la misma transacción, funciona.
//
// Cubre: RF10, I7
func TestReprogramarSolapandoConsigoMisma(t *testing.T) {
	e := NuevoEscenario(t)
	tok := Token(t, usuarioRecepcion)
	r := e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv45, e.Hora(0, 9, 0))
	require.Equal(t, http.StatusCreated, r.Status)
	var c citaJSON
	r.JSON(t, &c)
	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/reprogramar", tok, map[string]any{"inicio": e.Hora(0, 9, 30).Format(time.RFC3339)})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	require.Equal(t, 1, ContarVigentesQueCruzan(t, e.Prof, e.Hora(0, 9, 0), e.Hora(0, 10, 15)))
}
