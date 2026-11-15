// Package db concentra el acceso a PostgreSQL: el pool, la transacción por
// petición (perfil + clínica), los reintentos y la traducción de errores del motor.
package db

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
)

var perfiles = map[string]string{
	identidad.AdminPlataforma: "db_plataforma",
	identidad.AdminClinica:    "db_admin_clinica",
	identidad.Recepcion:       "db_recepcion",
	identidad.Profesional:     "db_profesional",
}

const maxIntentos = 3

type Store struct {
	Pool *pgxpool.Pool
}

func Conectar(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

type FilaIdentidad struct {
	UsuarioID     uuid.UUID
	ClinicaID     uuid.NullUUID
	Rol           string
	Activo        bool
	ClinicaActiva bool
}

func (s *Store) ResolverIdentidad(ctx context.Context, sub uuid.UUID) (*FilaIdentidad, error) {
	var f FilaIdentidad
	err := s.Pool.QueryRow(ctx,
		`SELECT usuario_id, clinica_id, rol_codigo, activo, clinica_activa FROM resolver_identidad($1)`, sub,
	).Scan(&f.UsuarioID, &f.ClinicaID, &f.Rol, &f.Activo, &f.ClinicaActiva)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) EnTransaccion(ctx context.Context, ident identidad.Identidad, fn func(tx pgx.Tx) error) error {
	for intento := 0; ; intento++ {
		err := s.unaVez(ctx, ident, fn)
		if err == nil {
			return nil
		}
		if !EsReintentable(err) {
			return Traducir(err)
		}
		if intento == maxIntentos-1 {
			slog.Warn("transacción abortada; se agotaron los reintentos", "error", err)
			return errores.Reintentar()
		}
		slog.Info("transacción abortada por el motor; se reintenta", "intento", intento+1, "error", err)
		espera := time.Duration(50<<intento)*time.Millisecond + time.Duration(rand.IntN(40))*time.Millisecond
		select {
		case <-time.After(espera):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Store) unaVez(ctx context.Context, ident identidad.Identidad, fn func(tx pgx.Tx) error) error {
	perfil, ok := perfiles[ident.Rol]
	if !ok {
		return errores.Prohibido("Rol sin perfil de base de datos")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // después de Commit no hace nada

	// perfil viene del mapa fijo de arriba, nunca del token.
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+perfil); err != nil {
		return err
	}
	clinica := ""
	if ident.ClinicaID.Valid {
		clinica = ident.ClinicaID.UUID.String()
	}
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.clinica_id', $1, true), set_config('app.usuario_id', $2, true)`,
		clinica, ident.UsuarioID.String()); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
