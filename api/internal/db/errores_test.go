package db

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"medicitas/api/internal/errores"
)

func TestTraducir(t *testing.T) {
	casos := []struct {
		code, restriccion string
		status            int
		codigo            string
	}{
		{"23P01", "cita_sin_solape_profesional", http.StatusConflict, "ESPACIO_TOMADO"},
		{"23P01", "cita_sin_solape_paciente", http.StatusConflict, "PACIENTE_OCUPADO"},
		{"23P01", "horario_sin_solape", http.StatusConflict, "HORARIO_SOLAPADO"},
		{"23505", "", http.StatusConflict, "DUPLICADO"},
		{"23503", "", http.StatusUnprocessableEntity, "REFERENCIA_INVALIDA"},
		{"23514", "", http.StatusUnprocessableEntity, "SOLICITUD_INVALIDA"},
		{"22P02", "", http.StatusUnprocessableEntity, "SOLICITUD_INVALIDA"},
		{"42501", "", http.StatusForbidden, "PROHIBIDO"},
		{"MC001", "", http.StatusUnprocessableEntity, "FUERA_DE_HORARIO"},
		{"MC002", "", http.StatusUnprocessableEntity, "EN_BLOQUEO"},
		{"MC003", "", http.StatusConflict, "TRANSICION_INVALIDA"},
		{"MC004", "", http.StatusUnprocessableEntity, "DESENLACE_PREMATURO"},
		{"MC005", "", http.StatusForbidden, "NOTA_NO_PERMITIDA"},
		{"MC006", "", http.StatusForbidden, "BITACORA_INMUTABLE"},
		{"MC007", "", http.StatusUnprocessableEntity, "CAMPO_INMUTABLE"},
		{"MC008", "", http.StatusUnprocessableEntity, "SERVICIO_NO_HABILITADO"},
		{"MC009", "", http.StatusUnprocessableEntity, "INACTIVO"},
		{"MC010", "", http.StatusForbidden, "PROHIBIDO"},
		{"MC011", "", http.StatusUnprocessableEntity, "SOLICITUD_INVALIDA"},
	}
	for _, c := range casos {
		t.Run(c.code+c.restriccion, func(t *testing.T) {
			err := fmt.Errorf("envuelto: %w", &pgconn.PgError{Code: c.code, ConstraintName: c.restriccion})
			e := errores.Como(Traducir(err))
			require.NotNil(t, e)
			require.Equal(t, c.status, e.Status)
			require.Equal(t, c.codigo, e.Codigo)
		})
	}
	desconocido := errors.New("se cayó la red")
	require.Equal(t, desconocido, Traducir(desconocido), "lo desconocido no se disfraza")
	require.True(t, EsReintentable(&pgconn.PgError{Code: "40P01"}))
	require.True(t, EsReintentable(&pgconn.PgError{Code: "40001"}))
	require.False(t, EsReintentable(&pgconn.PgError{Code: "23P01"}))
}
