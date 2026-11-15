package http

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"medicitas/api/internal/errores"
)

// Funciones comunes a los manejadores. Todo SQL va con parámetros $1, $2...;
// ninguna de estas funciones arma texto con datos del usuario.

func (d *Deps) enTx(r *http.Request, fn func(ctx context.Context, tx pgx.Tx) error) error {
	ctx := r.Context()
	return d.Store.EnTransaccion(ctx, Ident(r), func(tx pgx.Tx) error { return fn(ctx, tx) })
}

func lista[T any](ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]T, error) {
	filas, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(filas, pgx.RowToStructByName[T])
}

func una[T any](ctx context.Context, tx pgx.Tx, noEncontrado string, sql string, args ...any) (*T, error) {
	filas, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	v, err := pgx.CollectExactlyOneRow(filas, pgx.RowToAddrOfStructByName[T])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errores.NoEncontrado(noEncontrado)
	}
	return v, err
}

func existeID(ctx context.Context, tx pgx.Tx, sql string, id uuid.UUID, queEs string) error {
	var ok bool
	if err := tx.QueryRow(ctx, sql, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errores.NoEncontrado(queEs + " no encontrado")
	}
	return nil
}

func QueryUUIDOpcional(r *http.Request, nombre string) (uuid.NullUUID, error) {
	v := r.URL.Query().Get(nombre)
	if v == "" {
		return uuid.NullUUID{}, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.NullUUID{}, errores.Invalido(nombre + " no es un UUID válido")
	}
	return uuid.NullUUID{UUID: id, Valid: true}, nil
}

func zonaDeClinica(ctx context.Context, tx pgx.Tx) (*time.Location, error) {
	var nombre string
	if err := tx.QueryRow(ctx, `SELECT zona_horaria FROM clinica WHERE id = app_clinica_actual()`).Scan(&nombre); err != nil {
		return nil, err
	}
	return time.LoadLocation(nombre)
}

func instante(valor string, zona *time.Location, finDeDia bool) (*time.Time, error) {
	if valor == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, valor); err == nil {
		return &t, nil
	}
	d, err := time.ParseInLocation("2006-01-02", valor, zona)
	if err != nil {
		return nil, errores.Invalido("Las fechas van como AAAA-MM-DD o RFC 3339")
	}
	if finDeDia {
		d = d.AddDate(0, 0, 1)
	}
	return &d, nil
}

func paginacion(r *http.Request) (int, int, error) {
	limite, desplazamiento := 50, 0
	if v := r.URL.Query().Get("limite"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return 0, 0, errores.Invalido("limite debe estar entre 1 y 200")
		}
		limite = n
	}
	if v := r.URL.Query().Get("desplazamiento"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, 0, errores.Invalido("desplazamiento debe ser un entero no negativo")
		}
		desplazamiento = n
	}
	return limite, desplazamiento, nil
}

func requerido(campos map[string]string) error {
	var faltan []string
	for nombre, v := range campos {
		if strings.TrimSpace(v) == "" {
			faltan = append(faltan, nombre)
		}
	}
	if len(faltan) > 0 {
		slices.Sort(faltan)
		return errores.Invalido("Faltan campos obligatorios: " + strings.Join(faltan, ", "))
	}
	return nil
}

func manejar(fn func(r *http.Request) (int, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, v, err := fn(r)
		if err != nil {
			Error(w, err)
			return
		}
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		JSON(w, status, v)
	}
}
