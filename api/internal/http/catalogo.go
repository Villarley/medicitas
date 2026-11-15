package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"medicitas/api/internal/errores"
)

type especialidad struct {
	ID     uuid.UUID `db:"id" json:"id"`
	Nombre string    `db:"nombre" json:"nombre"`
}

func (d *Deps) listarEspecialidades(r *http.Request) (int, any, error) {
	var out []especialidad
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = lista[especialidad](ctx, tx, `SELECT id, nombre FROM especialidad ORDER BY nombre`)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) crearEspecialidad(r *http.Request) (int, any, error) {
	var s struct {
		Nombre string `json:"nombre"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if err := requerido(map[string]string{"nombre": s.Nombre}); err != nil {
		return 0, nil, err
	}
	var out *especialidad
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = una[especialidad](ctx, tx, "",
			`INSERT INTO especialidad (clinica_id, nombre) VALUES (app_clinica_actual(), $1) RETURNING id, nombre`,
			strings.TrimSpace(s.Nombre))
		return err
	})
	return http.StatusCreated, out, err
}

type servicio struct {
	ID             uuid.UUID `db:"id" json:"id"`
	Nombre         string    `db:"nombre" json:"nombre"`
	DuracionMin    int       `db:"duracion_min" json:"duracion_min"`
	EspecialidadID uuid.UUID `db:"especialidad_id" json:"especialidad_id"`
	Especialidad   string    `db:"especialidad" json:"especialidad"`
	Activo         bool      `db:"activo" json:"activo"`
}

const sqlServicio = `SELECT s.id, s.nombre, s.duracion_min, s.especialidad_id, e.nombre AS especialidad, s.activo
	FROM servicio s JOIN especialidad e ON e.id = s.especialidad_id`

func (d *Deps) listarServicios(r *http.Request) (int, any, error) {
	filtro := r.URL.Query().Get("activo")
	if filtro != "" && filtro != "true" && filtro != "false" {
		return 0, nil, errores.Invalido("activo debe ser true o false")
	}
	var out []servicio
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = lista[servicio](ctx, tx,
			sqlServicio+` WHERE ($1 = '' OR s.activo = ($1 = 'true')) ORDER BY s.nombre`, filtro)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) crearServicio(r *http.Request) (int, any, error) {
	var s struct {
		Nombre         string    `json:"nombre"`
		DuracionMin    int       `json:"duracion_min"`
		EspecialidadID uuid.UUID `json:"especialidad_id"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if err := requerido(map[string]string{"nombre": s.Nombre}); err != nil {
		return 0, nil, err
	}
	if s.EspecialidadID == uuid.Nil {
		return 0, nil, errores.Invalido("Falta especialidad_id")
	}
	if s.DuracionMin < 5 || s.DuracionMin > 480 || s.DuracionMin%5 != 0 {
		return 0, nil, errores.Invalido("duracion_min debe ser múltiplo de 5 entre 5 y 480")
	}
	var out *servicio
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM especialidad WHERE id = $1)`, s.EspecialidadID, "Especialidad"); err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO servicio (clinica_id, nombre, duracion_min, especialidad_id)
			 VALUES (app_clinica_actual(), $1, $2, $3) RETURNING id`,
			strings.TrimSpace(s.Nombre), s.DuracionMin, s.EspecialidadID).Scan(&id); err != nil {
			return err
		}
		var err error
		out, err = una[servicio](ctx, tx, "Servicio no encontrado", sqlServicio+` WHERE s.id = $1`, id)
		return err
	})
	return http.StatusCreated, out, err
}

func (d *Deps) actualizarServicio(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var s struct {
		Nombre      *string `json:"nombre"`
		Activo      *bool   `json:"activo"`
		DuracionMin *int    `json:"duracion_min"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if s.DuracionMin != nil {
		return 0, nil, errores.Nuevo(http.StatusUnprocessableEntity, "CAMPO_INMUTABLE",
			"La duración de un servicio no se modifica (I4); creá un servicio nuevo")
	}
	if s.Nombre == nil && s.Activo == nil {
		return 0, nil, errores.Invalido("Nada que actualizar: mandá nombre o activo")
	}
	if s.Nombre != nil && strings.TrimSpace(*s.Nombre) == "" {
		return 0, nil, errores.Invalido("nombre no puede quedar vacío")
	}
	var out *servicio
	var profs []uuid.UUID
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE servicio SET nombre = coalesce($2, nombre), activo = coalesce($3, activo) WHERE id = $1`,
			id, recortar(s.Nombre), s.Activo)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errores.NoEncontrado("Servicio no encontrado")
		}
		if s.Activo != nil {
			filas, err := tx.Query(ctx, `SELECT profesional_id FROM profesional_servicio WHERE servicio_id = $1`, id)
			if err != nil {
				return err
			}
			if profs, err = pgx.CollectRows(filas, pgx.RowTo[uuid.UUID]); err != nil {
				return err
			}
		}
		out, err = una[servicio](ctx, tx, "Servicio no encontrado", sqlServicio+` WHERE s.id = $1`, id)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	d.invalidarProfesionales(r, profs...)
	return http.StatusOK, out, nil
}

type profesional struct {
	ID             uuid.UUID     `db:"id" json:"id"`
	Nombre         string        `db:"nombre" json:"nombre"`
	EspecialidadID uuid.UUID     `db:"especialidad_id" json:"especialidad_id"`
	Especialidad   string        `db:"especialidad" json:"especialidad"`
	UsuarioID      uuid.NullUUID `db:"usuario_id" json:"usuario_id"`
	Activo         bool          `db:"activo" json:"activo"`
	ServicioIDs    []uuid.UUID   `db:"servicio_ids" json:"servicio_ids"`
}

const sqlProfesional = `SELECT p.id, p.nombre, p.especialidad_id, e.nombre AS especialidad, p.usuario_id, p.activo,
	coalesce((SELECT array_agg(ps.servicio_id ORDER BY ps.servicio_id) FROM profesional_servicio ps
	           WHERE ps.profesional_id = p.id), '{}') AS servicio_ids
	FROM profesional p JOIN especialidad e ON e.id = p.especialidad_id`

func (d *Deps) listarProfesionales(r *http.Request) (int, any, error) {
	srv, err := QueryUUIDOpcional(r, "servicio_id")
	if err != nil {
		return 0, nil, err
	}
	var out []profesional
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = lista[profesional](ctx, tx, sqlProfesional+`
			WHERE $1::uuid IS NULL OR EXISTS (SELECT 1 FROM profesional_servicio ps
			                                   WHERE ps.profesional_id = p.id AND ps.servicio_id = $1)
			ORDER BY p.nombre`, srv)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) obtenerProfesional(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var out *profesional
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = una[profesional](ctx, tx, "Profesional no encontrado", sqlProfesional+` WHERE p.id = $1`, id)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) crearProfesional(r *http.Request) (int, any, error) {
	var s struct {
		Nombre         string        `json:"nombre"`
		EspecialidadID uuid.UUID     `json:"especialidad_id"`
		UsuarioID      uuid.NullUUID `json:"usuario_id"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if err := requerido(map[string]string{"nombre": s.Nombre}); err != nil {
		return 0, nil, err
	}
	if s.EspecialidadID == uuid.Nil {
		return 0, nil, errores.Invalido("Falta especialidad_id")
	}
	var out *profesional
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM especialidad WHERE id = $1)`, s.EspecialidadID, "Especialidad"); err != nil {
			return err
		}
		if s.UsuarioID.Valid {
			var rol string
			var activo bool
			err := tx.QueryRow(ctx, `SELECT rol_codigo, activo FROM usuario WHERE id = $1`, s.UsuarioID.UUID).Scan(&rol, &activo)
			if errors.Is(err, pgx.ErrNoRows) {
				return errores.NoEncontrado("Usuario no encontrado")
			}
			if err != nil {
				return err
			}
			if rol != "profesional" || !activo {
				return errores.Invalido("usuario_id debe ser un usuario activo con rol profesional")
			}
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO profesional (clinica_id, nombre, especialidad_id, usuario_id)
			 VALUES (app_clinica_actual(), $1, $2, $3) RETURNING id`,
			strings.TrimSpace(s.Nombre), s.EspecialidadID, s.UsuarioID).Scan(&id); err != nil {
			return err
		}
		var err error
		out, err = una[profesional](ctx, tx, "Profesional no encontrado", sqlProfesional+` WHERE p.id = $1`, id)
		return err
	})
	return http.StatusCreated, out, err
}

func (d *Deps) asignarServicios(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var s struct {
		ServicioIDs []uuid.UUID `json:"servicio_ids"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if s.ServicioIDs == nil {
		return 0, nil, errores.Invalido("Falta servicio_ids (puede ser una lista vacía)")
	}
	var out *profesional
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var esp uuid.UUID
		// FOR UPDATE: dos asignaciones simultáneas al mismo profesional se ordenan.
		err := tx.QueryRow(ctx, `SELECT especialidad_id FROM profesional WHERE id = $1 FOR UPDATE`, id).Scan(&esp)
		if errors.Is(err, pgx.ErrNoRows) {
			return errores.NoEncontrado("Profesional no encontrado")
		}
		if err != nil {
			return err
		}
		var encontrados, deOtraEsp int
		if err := tx.QueryRow(ctx,
			`SELECT count(*), count(*) FILTER (WHERE especialidad_id <> $2) FROM servicio WHERE id = ANY($1::uuid[])`,
			s.ServicioIDs, esp).Scan(&encontrados, &deOtraEsp); err != nil {
			return err
		}
		if encontrados != len(unicos(s.ServicioIDs)) {
			return errores.NoEncontrado("Algún servicio no existe")
		}
		if deOtraEsp > 0 {
			return errores.Nuevo(http.StatusUnprocessableEntity, "ESPECIALIDAD_NO_COINCIDE",
				"El profesional solo atiende servicios de su especialidad")
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM profesional_servicio WHERE profesional_id = $1 AND NOT (servicio_id = ANY($2::uuid[]))`,
			id, s.ServicioIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO profesional_servicio (clinica_id, profesional_id, servicio_id)
			 SELECT app_clinica_actual(), $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`,
			id, s.ServicioIDs); err != nil {
			return err
		}
		out, err = una[profesional](ctx, tx, "Profesional no encontrado", sqlProfesional+` WHERE p.id = $1`, id)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	d.invalidarProfesionales(r, id)
	return http.StatusOK, out, nil
}

func (d *Deps) invalidarProfesionales(r *http.Request, profs ...uuid.UUID) {
	ident := Ident(r)
	if d.Cache == nil || !ident.ClinicaID.Valid {
		return
	}
	for _, p := range profs {
		d.Cache.InvalidarProfesional(r.Context(), ident.ClinicaID.UUID, p)
	}
}

func unicos(ids []uuid.UUID) map[uuid.UUID]struct{} {
	m := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func recortar(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}
