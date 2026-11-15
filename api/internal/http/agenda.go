package http

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
)

func soloPropio(ctx context.Context, tx pgx.Tx, ident identidad.Identidad, prof uuid.UUID) error {
	if ident.Rol != identidad.Profesional {
		return nil
	}
	var mio uuid.NullUUID
	if err := tx.QueryRow(ctx, `SELECT mi_profesional()`).Scan(&mio); err != nil {
		return err
	}
	if !mio.Valid || mio.UUID != prof {
		return errores.Prohibido("Un profesional solo gestiona su propia agenda")
	}
	return nil
}

type horario struct {
	ID            uuid.UUID `db:"id" json:"id"`
	ProfesionalID uuid.UUID `db:"profesional_id" json:"profesional_id"`
	DiaSemana     int16     `db:"dia_semana" json:"dia_semana"`
	HoraInicio    string    `db:"hora_inicio" json:"hora_inicio"`
	HoraFin       string    `db:"hora_fin" json:"hora_fin"`
}

const sqlHorario = `SELECT id, profesional_id, dia_semana,
	to_char(hora_inicio, 'HH24:MI') AS hora_inicio, to_char(hora_fin, 'HH24:MI') AS hora_fin
	FROM horario_atencion`

func (d *Deps) listarHorarios(r *http.Request) (int, any, error) {
	prof, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var out []horario
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, prof, "Profesional"); err != nil {
			return err
		}
		if err := soloPropio(ctx, tx, Ident(r), prof); err != nil {
			return err
		}
		var err error
		out, err = lista[horario](ctx, tx, sqlHorario+` WHERE profesional_id = $1 ORDER BY dia_semana, hora_inicio`, prof)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) crearHorario(r *http.Request) (int, any, error) {
	prof, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var s struct {
		DiaSemana  int    `json:"dia_semana"`
		HoraInicio string `json:"hora_inicio"`
		HoraFin    string `json:"hora_fin"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if s.DiaSemana < 1 || s.DiaSemana > 7 {
		return 0, nil, errores.Invalido("dia_semana va de 1 (lunes) a 7 (domingo)")
	}
	hi, err1 := time.Parse("15:04", s.HoraInicio)
	hf, err2 := time.Parse("15:04", s.HoraFin)
	if err1 != nil || err2 != nil {
		return 0, nil, errores.Invalido("hora_inicio y hora_fin van como HH:MM")
	}
	if !hf.After(hi) {
		return 0, nil, errores.Invalido("hora_fin tiene que ser posterior a hora_inicio")
	}
	var out *horario
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, prof, "Profesional"); err != nil {
			return err
		}
		if err := soloPropio(ctx, tx, Ident(r), prof); err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO horario_atencion (clinica_id, profesional_id, dia_semana, hora_inicio, hora_fin)
			 VALUES (app_clinica_actual(), $1, $2, $3::time, $4::time) RETURNING id`,
			prof, s.DiaSemana, s.HoraInicio, s.HoraFin).Scan(&id); err != nil {
			return err
		}
		var err error
		out, err = una[horario](ctx, tx, "Horario no encontrado", sqlHorario+` WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	d.invalidarProfesionales(r, prof)
	return http.StatusCreated, out, nil
}

func (d *Deps) borrarHorario(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var prof uuid.UUID
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var dia int16
		var hi, hf string
		// Lo de otra clínica, o el horario de otro profesional, no aparece: 0 filas = 404.
		err := tx.QueryRow(ctx,
			`DELETE FROM horario_atencion WHERE id = $1
			 RETURNING profesional_id, dia_semana, hora_inicio::text, hora_fin::text`, id).Scan(&prof, &dia, &hi, &hf)
		if errors.Is(err, pgx.ErrNoRows) {
			return errores.NoEncontrado("Horario no encontrado")
		}
		if err != nil {
			return err
		}
		if err := soloPropio(ctx, tx, Ident(r), prof); err != nil {
			return err
		}
		var dentro int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM cita c JOIN clinica cl ON cl.id = c.clinica_id
			  WHERE c.profesional_id = $1 AND c.estado = 'reservada' AND c.fin > now()
			    AND extract(isodow FROM c.inicio AT TIME ZONE cl.zona_horaria) = $2
			    AND (c.inicio AT TIME ZONE cl.zona_horaria)::time < $4::time
			    AND (c.fin    AT TIME ZONE cl.zona_horaria)::time > $3::time`,
			prof, dia, hi, hf).Scan(&dentro); err != nil {
			return err
		}
		if dentro > 0 {
			return errores.Conflicto("HORARIO_CON_CITAS",
				"Ese bloque tiene citas vigentes; reprogramalas antes de quitarlo")
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	d.invalidarProfesionales(r, prof)
	return http.StatusNoContent, nil, nil
}

type bloqueo struct {
	ID            uuid.UUID `db:"id" json:"id"`
	ProfesionalID uuid.UUID `db:"profesional_id" json:"profesional_id"`
	Inicio        time.Time `db:"inicio" json:"inicio"`
	Fin           time.Time `db:"fin" json:"fin"`
	Motivo        string    `db:"motivo" json:"motivo"`
	CreadoPor     uuid.UUID `db:"creado_por" json:"creado_por"`
	CreadoEn      time.Time `db:"creado_en" json:"creado_en"`
}

const sqlBloqueo = `SELECT id, profesional_id, inicio, fin, motivo, creado_por, creado_en FROM bloqueo_agenda`

type citaDetalle struct {
	ID                uuid.UUID     `db:"id" json:"id"`
	PacienteID        uuid.UUID     `db:"paciente_id" json:"paciente_id"`
	Paciente          string        `db:"paciente" json:"paciente"`
	ProfesionalID     uuid.UUID     `db:"profesional_id" json:"profesional_id"`
	Profesional       string        `db:"profesional" json:"profesional"`
	ServicioID        uuid.UUID     `db:"servicio_id" json:"servicio_id"`
	Servicio          string        `db:"servicio" json:"servicio"`
	Inicio            time.Time     `db:"inicio" json:"inicio"`
	Fin               time.Time     `db:"fin" json:"fin"`
	Estado            string        `db:"estado" json:"estado"`
	MotivoCancelacion *string       `db:"motivo_cancelacion" json:"motivo_cancelacion"`
	CitaOrigenID      uuid.NullUUID `db:"cita_origen_id" json:"cita_origen_id"`
}

const sqlCitaDetalle = `SELECT c.id, c.paciente_id, pa.nombre || ' ' || pa.apellidos AS paciente,
	c.profesional_id, pr.nombre AS profesional, c.servicio_id, s.nombre AS servicio,
	c.inicio, c.fin, c.estado::text AS estado, c.motivo_cancelacion, c.cita_origen_id
	FROM cita c
	JOIN paciente pa    ON pa.id = c.paciente_id
	JOIN profesional pr ON pr.id = c.profesional_id
	JOIN servicio s     ON s.id = c.servicio_id`

func (d *Deps) listarBloqueos(r *http.Request) (int, any, error) {
	prof, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var out []bloqueo
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, prof, "Profesional"); err != nil {
			return err
		}
		if err := soloPropio(ctx, tx, Ident(r), prof); err != nil {
			return err
		}
		zona, err := zonaDeClinica(ctx, tx)
		if err != nil {
			return err
		}
		desde, err := instante(r.URL.Query().Get("desde"), zona, false)
		if err != nil {
			return err
		}
		hasta, err := instante(r.URL.Query().Get("hasta"), zona, true)
		if err != nil {
			return err
		}
		out, err = lista[bloqueo](ctx, tx, sqlBloqueo+`
			WHERE profesional_id = $1 AND ($2::timestamptz IS NULL OR fin > $2) AND ($3::timestamptz IS NULL OR inicio < $3)
			ORDER BY inicio`, prof, desde, hasta)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) crearBloqueo(r *http.Request) (int, any, error) {
	prof, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var s struct {
		Inicio time.Time `json:"inicio"`
		Fin    time.Time `json:"fin"`
		Motivo string    `json:"motivo"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if s.Inicio.IsZero() || s.Fin.IsZero() {
		return 0, nil, errores.Invalido("Faltan inicio o fin (ISO 8601 con zona)")
	}
	if !s.Fin.After(s.Inicio) {
		return 0, nil, errores.Invalido("fin tiene que ser posterior a inicio")
	}
	if err := requerido(map[string]string{"motivo": s.Motivo}); err != nil {
		return 0, nil, err
	}
	var out struct {
		Bloqueo    *bloqueo      `json:"bloqueo"`
		Conflictos []citaDetalle `json:"conflictos"`
	}
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, prof, "Profesional"); err != nil {
			return err
		}
		if err := soloPropio(ctx, tx, Ident(r), prof); err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO bloqueo_agenda (clinica_id, profesional_id, inicio, fin, motivo, creado_por)
			 VALUES (app_clinica_actual(), $1, $2, $3, $4, app_usuario_actual()) RETURNING id`,
			prof, s.Inicio, s.Fin, strings.TrimSpace(s.Motivo)).Scan(&id); err != nil {
			return err
		}
		var err error
		if out.Bloqueo, err = una[bloqueo](ctx, tx, "Bloqueo no encontrado", sqlBloqueo+` WHERE id = $1`, id); err != nil {
			return err
		}
		out.Conflictos, err = lista[citaDetalle](ctx, tx, sqlCitaDetalle+`
			WHERE c.profesional_id = $1 AND c.estado = 'reservada'
			  AND tstzrange(c.inicio, c.fin, '[)') && tstzrange($2, $3, '[)')
			ORDER BY c.inicio`, prof, s.Inicio, s.Fin)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	d.invalidarProfesionales(r, prof)
	return http.StatusCreated, out, nil
}

func (d *Deps) borrarBloqueo(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var prof uuid.UUID
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `DELETE FROM bloqueo_agenda WHERE id = $1 RETURNING profesional_id`, id).Scan(&prof)
		if errors.Is(err, pgx.ErrNoRows) {
			return errores.NoEncontrado("Bloqueo no encontrado")
		}
		if err != nil {
			return err
		}
		return soloPropio(ctx, tx, Ident(r), prof)
	})
	if err != nil {
		return 0, nil, err
	}
	d.invalidarProfesionales(r, prof)
	return http.StatusNoContent, nil, nil
}

func (d *Deps) listarConflictos(r *http.Request) (int, any, error) {
	prof, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var out []citaDetalle
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, prof, "Profesional"); err != nil {
			return err
		}
		if err := soloPropio(ctx, tx, Ident(r), prof); err != nil {
			return err
		}
		var err error
		out, err = lista[citaDetalle](ctx, tx, sqlCitaDetalle+`
			WHERE c.profesional_id = $1 AND c.estado = 'reservada'
			  AND EXISTS (SELECT 1 FROM bloqueo_agenda b
			               WHERE b.profesional_id = c.profesional_id
			                 AND tstzrange(b.inicio, b.fin, '[)') && tstzrange(c.inicio, c.fin, '[)'))
			ORDER BY c.inicio`, prof)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) agendaDelDia(r *http.Request) (int, any, error) {
	prof, err := QueryUUIDOpcional(r, "profesional_id")
	if err != nil {
		return 0, nil, err
	}
	fecha := r.URL.Query().Get("fecha")
	if fecha == "" {
		return 0, nil, errores.Invalido("Falta el parámetro fecha (AAAA-MM-DD)")
	}
	ident := Ident(r)
	var out []citaDetalle
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		zona, err := zonaDeClinica(ctx, tx)
		if err != nil {
			return err
		}
		dia, err := time.ParseInLocation("2006-01-02", fecha, zona)
		if err != nil {
			return errores.Invalido("fecha debe tener formato AAAA-MM-DD")
		}
		if ident.Rol == identidad.Profesional && !prof.Valid {
			if err := tx.QueryRow(ctx, `SELECT mi_profesional()`).Scan(&prof); err != nil {
				return err
			}
			if !prof.Valid {
				return errores.Prohibido("El usuario no está vinculado a un profesional")
			}
		}
		if prof.Valid {
			if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM profesional WHERE id = $1)`, prof.UUID, "Profesional"); err != nil {
				return err
			}
			if err := soloPropio(ctx, tx, ident, prof.UUID); err != nil {
				return err
			}
		}
		out, err = lista[citaDetalle](ctx, tx, sqlCitaDetalle+`
			WHERE c.inicio >= $1 AND c.inicio < $2 AND ($3::uuid IS NULL OR c.profesional_id = $3)
			ORDER BY c.inicio, pr.nombre`, dia, dia.AddDate(0, 0, 1), prof)
		return err
	})
	return http.StatusOK, out, err
}
