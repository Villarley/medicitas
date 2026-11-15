package db

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"medicitas/api/internal/errores"
)

func EsReintentable(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "40P01" || pg.Code == "40001"
	}
	return false
}

func Traducir(err error) error {
	if err == nil {
		return nil
	}
	if e := errores.Como(err); e != nil {
		return e
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch pg.Code {
	case "23P01":
		switch pg.ConstraintName {
		case "cita_sin_solape_profesional":
			return errores.Conflicto("ESPACIO_TOMADO", "Ese espacio ya está tomado")
		case "cita_sin_solape_paciente":
			return errores.Conflicto("PACIENTE_OCUPADO", "El paciente ya tiene una cita a esa hora")
		case "horario_sin_solape":
			return errores.Conflicto("HORARIO_SOLAPADO", "El bloque se cruza con otro del mismo día")
		}
		return errores.Conflicto("CONFLICTO", "La operación choca con datos existentes")
	case "23505":
		return errores.Conflicto("DUPLICADO", "Ya existe un registro con esos datos")
	case "23503":
		return errores.Nuevo(http.StatusUnprocessableEntity, "REFERENCIA_INVALIDA", "Hace referencia a algo que no existe")
	case "23514", "23502", "22P02", "22007", "22008", "22001", "22023":
		return errores.Invalido("Dato inválido")
	case "42501":
		return errores.Prohibido("Sin permiso para esta operación")
	case "MC001":
		return errores.Nuevo(http.StatusUnprocessableEntity, "FUERA_DE_HORARIO", "La cita cae fuera del horario de atención")
	case "MC002":
		return errores.Nuevo(http.StatusUnprocessableEntity, "EN_BLOQUEO", "La cita cae dentro de un bloqueo de agenda")
	case "MC003":
		return errores.Conflicto("TRANSICION_INVALIDA", pg.Message)
	case "MC004":
		return errores.Nuevo(http.StatusUnprocessableEntity, "DESENLACE_PREMATURO", "El desenlace se registra después de la hora de inicio")
	case "MC005":
		return errores.Nuevo(http.StatusForbidden, "NOTA_NO_PERMITIDA", pg.Message)
	case "MC006":
		return errores.Nuevo(http.StatusForbidden, "BITACORA_INMUTABLE", "La bitácora no se modifica")
	case "MC007":
		return errores.Nuevo(http.StatusUnprocessableEntity, "CAMPO_INMUTABLE", pg.Message)
	case "MC008":
		return errores.Nuevo(http.StatusUnprocessableEntity, "SERVICIO_NO_HABILITADO", "El profesional no atiende ese servicio")
	case "MC009":
		return errores.Nuevo(http.StatusUnprocessableEntity, "INACTIVO", "Profesional o servicio inactivo")
	case "MC010":
		return errores.Prohibido("El profesional solo registra el desenlace")
	case "MC011":
		return errores.Invalido("Zona horaria inválida")
	}
	return err
}
