package pruebas

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// TestBitacoraInmutable: ni con el perfil de admin de clínica se puede cambiar
// o borrar la bitácora. Lo impide el motor (sin permisos y con trigger), no la API.
//
// Cubre: RF18, I6
func TestBitacoraInmutable(t *testing.T) {
	e := NuevoEscenario(t)
	// Una entrada legítima, escrita como la escribiría la API.
	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO auditoria (clinica_id, usuario_id, accion, recurso, paciente_id)
			 VALUES (app_clinica_actual(), app_usuario_actual(), 'lectura', 'contacto_paciente', $1)`, e.Pacientes[0])
		return err
	})
	for _, sql := range []string{
		`UPDATE auditoria SET detalle = 'alterado' WHERE paciente_id = $1`,
		`DELETE FROM auditoria WHERE paciente_id = $1`,
	} {
		err := intentarComoAdmin(t, sql, e.Pacientes[0])
		require.Error(t, err, sql)
		require.Contains(t, err.Error(), "permission denied", sql)
	}
}

func intentarComoAdmin(t *testing.T, sql string, arg uuid.UUID) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	defer conn.Close(ctx)
	var usuarioID, clinicaID uuid.UUID
	require.NoError(t, conn.QueryRow(ctx, `SELECT usuario_id, clinica_id FROM resolver_identidad($1)`,
		subDelToken(t, Token(t, usuarioAdmin))).Scan(&usuarioID, &clinicaID))
	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, _ = tx.Exec(ctx, "SET LOCAL ROLE db_admin_clinica")
	_, _ = tx.Exec(ctx, `SELECT set_config('app.clinica_id', $1, true)`, clinicaID.String())
	_, err = tx.Exec(ctx, sql, arg)
	return err
}
