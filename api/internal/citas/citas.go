// Package citas implementa el ciclo de vida de la cita.
// Las reglas duras (solapes, horario, estados, duración) las garantiza el
// motor; aquí se arma la transacción, se validan entradas y se invalida la caché.
package citas

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"medicitas/api/internal/cache"
	"medicitas/api/internal/db"
	"medicitas/api/internal/disponibilidad"
	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
)

type Cita struct {
	ID                uuid.UUID     `json:"id"`
	ClinicaID         uuid.UUID     `json:"clinica_id"`
	PacienteID        uuid.UUID     `json:"paciente_id"`
	ProfesionalID     uuid.UUID     `json:"profesional_id"`
	ServicioID        uuid.UUID     `json:"servicio_id"`
	Inicio            time.Time     `json:"inicio"`
	Fin               time.Time     `json:"fin"`
	Estado            string        `json:"estado"`
	MotivoCancelacion *string       `json:"motivo_cancelacion"`
	CitaOrigenID      uuid.NullUUID `json:"cita_origen_id"`
	CreadaPor         uuid.UUID     `json:"creada_por"`
	CreadaEn          time.Time     `json:"creada_en"`
	CerradaPor        uuid.NullUUID `json:"cerrada_por"`
	CerradaEn         *time.Time    `json:"cerrada_en"`
}

const Columnas = `id, clinica_id, paciente_id, profesional_id, servicio_id, inicio, fin, estado::text,
	motivo_cancelacion, cita_origen_id, creada_por, creada_en, cerrada_por, cerrada_en`

func escanear(f pgx.Row) (*Cita, error) {
	var c Cita
	err := f.Scan(&c.ID, &c.ClinicaID, &c.PacienteID, &c.ProfesionalID, &c.ServicioID, &c.Inicio, &c.Fin,
		&c.Estado, &c.MotivoCancelacion, &c.CitaOrigenID, &c.CreadaPor, &c.CreadaEn, &c.CerradaPor, &c.CerradaEn)
	return &c, err
}

func EscanearFilas(filas pgx.Rows) ([]Cita, error) {
	return pgx.CollectRows(filas, func(f pgx.CollectableRow) (Cita, error) {
		c, err := escanear(f)
		if err != nil {
			return Cita{}, err
		}
		return *c, nil
	})
}

type Servicio struct {
	Store *db.Store
	Cache *cache.Cache
	Ahora func() time.Time
}

func (s *Servicio) ahora() time.Time {
	if s.Ahora != nil {
		return s.Ahora()
	}
	return time.Now()
}

type SolicitudReserva struct {
	PacienteID    uuid.UUID `json:"paciente_id"`
	ProfesionalID uuid.UUID `json:"profesional_id"`
	ServicioID    uuid.UUID `json:"servicio_id"`
	Inicio        time.Time `json:"inicio"`
}

func (r SolicitudReserva) validar(ahora time.Time) error {
	if r.PacienteID == uuid.Nil || r.ProfesionalID == uuid.Nil || r.ServicioID == uuid.Nil || r.Inicio.IsZero() {
		return errores.Invalido("Faltan paciente_id, profesional_id, servicio_id o inicio")
	}
	if !r.Inicio.After(ahora) {
		return errores.Invalido("La cita tiene que empezar en el futuro")
	}
	return nil
}

type toque struct {
	prof  uuid.UUID
	fecha string
}

func (s *Servicio) invalidar(ctx context.Context, clinica uuid.UUID, toques ...toque) {
	if s.Cache == nil {
		return
	}
	for _, t := range toques {
		s.Cache.InvalidarFecha(ctx, clinica, t.prof, t.fecha)
	}
}

func zonaClinica(ctx context.Context, tx pgx.Tx) (*time.Location, error) {
	var nombre string
	if err := tx.QueryRow(ctx, `SELECT zona_horaria FROM clinica WHERE id = app_clinica_actual()`).Scan(&nombre); err != nil {
		return nil, err
	}
	return time.LoadLocation(nombre)
}

func existe(ctx context.Context, tx pgx.Tx, sql string, id uuid.UUID, queEs string) error {
	var ok bool
	if err := tx.QueryRow(ctx, sql, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errores.NoEncontrado(queEs + " no encontrado")
	}
	return nil
}

func (s *Servicio) Reservar(ctx context.Context, ident identidad.Identidad, r SolicitudReserva) (*Cita, error) {
	if err := r.validar(s.ahora()); err != nil {
		return nil, err
	}
	var cita *Cita
	var zona *time.Location
	err := s.Store.EnTransaccion(ctx, ident, func(tx pgx.Tx) error {
		var err error
		if zona, err = zonaClinica(ctx, tx); err != nil {
			return err
		}
		// Con RLS, un id de otra clínica simplemente no existe.
		if err := existe(ctx, tx, `SELECT EXISTS (SELECT 1 FROM paciente WHERE id = $1)`, r.PacienteID, "Paciente"); err != nil {
			return err
		}
		if err := existe(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, r.ProfesionalID, "Profesional"); err != nil {
			return err
		}
		if err := existe(ctx, tx, `SELECT EXISTS (SELECT 1 FROM servicio WHERE id = $1)`, r.ServicioID, "Servicio"); err != nil {
			return err
		}
		// fin se manda igual a inicio: el trigger lo recalcula con la duración del servicio.
		cita, err = escanear(tx.QueryRow(ctx,
			`INSERT INTO cita (clinica_id, paciente_id, profesional_id, servicio_id, inicio, fin, creada_por)
			 VALUES (app_clinica_actual(), $1, $2, $3, $4, $4, app_usuario_actual())
			 RETURNING `+Columnas,
			r.PacienteID, r.ProfesionalID, r.ServicioID, r.Inicio))
		return err
	})
	if err != nil {
		return nil, err
	}
	s.invalidar(ctx, cita.ClinicaID, toque{cita.ProfesionalID, disponibilidad.FechaLocal(cita.Inicio, zona)})
	return cita, nil
}

func (s *Servicio) Obtener(ctx context.Context, ident identidad.Identidad, id uuid.UUID) (*Cita, error) {
	var cita *Cita
	err := s.Store.EnTransaccion(ctx, ident, func(tx pgx.Tx) error {
		var err error
		cita, err = escanear(tx.QueryRow(ctx, `SELECT `+Columnas+` FROM cita WHERE id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return errores.NoEncontrado("Cita no encontrada")
		}
		return err
	})
	return cita, err
}

type SolicitudReprogramar struct {
	Inicio        time.Time     `json:"inicio"`
	ProfesionalID uuid.NullUUID `json:"profesional_id"`
}

func (s *Servicio) Reprogramar(ctx context.Context, ident identidad.Identidad, id uuid.UUID, r SolicitudReprogramar) (*Cita, error) {
	if r.Inicio.IsZero() {
		return nil, errores.Invalido("Falta inicio")
	}
	if !r.Inicio.After(s.ahora()) {
		return nil, errores.Invalido("La cita tiene que empezar en el futuro")
	}
	var vieja, nueva *Cita
	var zona *time.Location
	err := s.Store.EnTransaccion(ctx, ident, func(tx pgx.Tx) error {
		var err error
		if zona, err = zonaClinica(ctx, tx); err != nil {
			return err
		}
		vieja, err = escanear(tx.QueryRow(ctx, `SELECT `+Columnas+` FROM cita WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return errores.NoEncontrado("Cita no encontrada")
		}
		if err != nil {
			return err
		}
		if vieja.Estado != "reservada" {
			return errores.Conflicto("TRANSICION_INVALIDA", "Solo se reprograma una cita reservada")
		}
		prof := vieja.ProfesionalID
		if r.ProfesionalID.Valid {
			prof = r.ProfesionalID.UUID
			if err := existe(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, prof, "Profesional"); err != nil {
				return err
			}
		}
		// Primero se libera el espacio viejo, así la nueva puede cruzarse con él.
		if _, err := tx.Exec(ctx, `UPDATE cita SET estado = 'reprogramada' WHERE id = $1`, id); err != nil {
			return err
		}
		nueva, err = escanear(tx.QueryRow(ctx,
			`INSERT INTO cita (clinica_id, paciente_id, profesional_id, servicio_id, inicio, fin, creada_por, cita_origen_id)
			 VALUES (app_clinica_actual(), $1, $2, $3, $4, $4, app_usuario_actual(), $5)
			 RETURNING `+Columnas,
			vieja.PacienteID, prof, vieja.ServicioID, r.Inicio, id))
		return err
	})
	if err != nil {
		return nil, err
	}
	s.invalidar(ctx, nueva.ClinicaID,
		toque{vieja.ProfesionalID, disponibilidad.FechaLocal(vieja.Inicio, zona)},
		toque{nueva.ProfesionalID, disponibilidad.FechaLocal(nueva.Inicio, zona)})
	return nueva, nil
}

func (s *Servicio) Cancelar(ctx context.Context, ident identidad.Identidad, id uuid.UUID, motivo string) (*Cita, error) {
	motivo = strings.TrimSpace(motivo)
	if motivo == "" {
		return nil, errores.Invalido("El motivo de cancelación es obligatorio")
	}
	return s.cerrar(ctx, ident, id, "cancelada", &motivo, true)
}

func (s *Servicio) RegistrarDesenlace(ctx context.Context, ident identidad.Identidad, id uuid.UUID, desenlace string) (*Cita, error) {
	if desenlace != "atendida" && desenlace != "ausente" {
		return nil, errores.Invalido("desenlace debe ser 'atendida' o 'ausente'")
	}
	return s.cerrar(ctx, ident, id, desenlace, nil, false)
}

func (s *Servicio) cerrar(ctx context.Context, ident identidad.Identidad, id uuid.UUID, estado string, motivo *string, liberaEspacio bool) (*Cita, error) {
	var cita *Cita
	var zona *time.Location
	err := s.Store.EnTransaccion(ctx, ident, func(tx pgx.Tx) error {
		var err error
		if zona, err = zonaClinica(ctx, tx); err != nil {
			return err
		}
		// Se distingue "no existe" de "transición inválida" para responder 404 o 409.
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cita WHERE id = $1)`, id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return errores.NoEncontrado("Cita no encontrada")
		}
		if motivo != nil {
			cita, err = escanear(tx.QueryRow(ctx,
				`UPDATE cita SET estado = $2::estado_cita, motivo_cancelacion = $3 WHERE id = $1 RETURNING `+Columnas,
				id, estado, *motivo))
		} else {
			cita, err = escanear(tx.QueryRow(ctx,
				`UPDATE cita SET estado = $2::estado_cita WHERE id = $1 RETURNING `+Columnas, id, estado))
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if liberaEspacio {
		s.invalidar(ctx, cita.ClinicaID, toque{cita.ProfesionalID, disponibilidad.FechaLocal(cita.Inicio, zona)})
	}
	return cita, nil
}
