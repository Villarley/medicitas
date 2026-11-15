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
)

// auditar escribe en la bitácora dentro de la misma transacción que la
// operación: si la operación confirma, su registro también; si algo falla,
// ninguno queda. usuario_id y ocurrido_en los pone un trigger desde
// app.usuario_id, así que la aplicación no puede atribuir el acceso a otra persona.

const (
	accionLectura   = "lectura"
	accionEscritura = "escritura"
	recursoContacto = "contacto_paciente"
	recursoNota     = "nota_clinica"
)

func auditar(ctx context.Context, tx pgx.Tx, accion, recurso string, paciente uuid.UUID, recursoID *uuid.UUID, detalle string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO auditoria (clinica_id, usuario_id, accion, recurso, paciente_id, recurso_id, detalle)
		 VALUES (app_clinica_actual(), app_usuario_actual(), $1::accion_auditoria, $2::recurso_auditoria, $3, $4, nullif($5, ''))`,
		accion, recurso, paciente, recursoID, detalle)
	return err
}

type pacienteResumen struct {
	ID              uuid.UUID `db:"id" json:"id"`
	Identificacion  string    `db:"identificacion" json:"identificacion"`
	Nombre          string    `db:"nombre" json:"nombre"`
	Apellidos       string    `db:"apellidos" json:"apellidos"`
	FechaNacimiento string    `db:"fecha_nacimiento" json:"fecha_nacimiento"`
}

type paciente struct {
	pacienteResumen
	Telefono      *string   `db:"telefono" json:"telefono"`
	Correo        *string   `db:"correo" json:"correo"`
	CreadoEn      time.Time `db:"creado_en" json:"creado_en"`
	ActualizadoEn time.Time `db:"actualizado_en" json:"actualizado_en"`
}

func (p *paciente) tieneContacto() bool { return p.Telefono != nil || p.Correo != nil }

const sqlPaciente = `SELECT id, identificacion, nombre, apellidos, to_char(fecha_nacimiento, 'YYYY-MM-DD') AS fecha_nacimiento,
	telefono, correo, creado_en, actualizado_en FROM paciente`

func (d *Deps) listarPacientes(r *http.Request) (int, any, error) {
	limite, desplazamiento, err := paginacion(r)
	if err != nil {
		return 0, nil, err
	}
	buscar := strings.TrimSpace(r.URL.Query().Get("buscar"))
	var out []pacienteResumen
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = lista[pacienteResumen](ctx, tx,
			`SELECT id, identificacion, nombre, apellidos, to_char(fecha_nacimiento, 'YYYY-MM-DD') AS fecha_nacimiento
			   FROM paciente
			  WHERE $1 = '' OR identificacion = $1
			     OR strpos(lower(nombre || ' ' || apellidos), lower($1)) > 0
			  ORDER BY apellidos, nombre, id
			  LIMIT $2 OFFSET $3`, buscar, limite, desplazamiento)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) obtenerPaciente(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var out *paciente
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if out, err = una[paciente](ctx, tx, "Paciente no encontrado", sqlPaciente+` WHERE id = $1`, id); err != nil {
			return err
		}
		if out.tieneContacto() {
			return auditar(ctx, tx, accionLectura, recursoContacto, id, nil, "GET /pacientes/{id}")
		}
		return nil
	})
	return http.StatusOK, out, err
}

type solicitudPaciente struct {
	Identificacion  *string `json:"identificacion"`
	Nombre          *string `json:"nombre"`
	Apellidos       *string `json:"apellidos"`
	FechaNacimiento *string `json:"fecha_nacimiento"`
	// Contacto: "" en PATCH lo borra.
	Telefono *string `json:"telefono"`
	Correo   *string `json:"correo"`
}

func (s *solicitudPaciente) validar(alta bool) error {
	if alta {
		faltan := map[string]string{}
		for nombre, v := range map[string]*string{"identificacion": s.Identificacion, "nombre": s.Nombre,
			"apellidos": s.Apellidos, "fecha_nacimiento": s.FechaNacimiento} {
			if v == nil {
				faltan[nombre] = ""
			} else {
				faltan[nombre] = *v
			}
		}
		if err := requerido(faltan); err != nil {
			return err
		}
	}
	for nombre, v := range map[string]*string{"identificacion": s.Identificacion, "nombre": s.Nombre, "apellidos": s.Apellidos} {
		if v != nil && strings.TrimSpace(*v) == "" {
			return errores.Invalido(nombre + " no puede quedar vacío")
		}
	}
	if s.FechaNacimiento != nil {
		f, err := time.Parse("2006-01-02", *s.FechaNacimiento)
		if err != nil {
			return errores.Invalido("fecha_nacimiento debe tener formato AAAA-MM-DD")
		}
		if f.After(time.Now()) {
			return errores.Invalido("fecha_nacimiento no puede estar en el futuro")
		}
	}
	return nil
}

func (s *solicitudPaciente) tocaContacto() string {
	var campos []string
	if s.Telefono != nil {
		campos = append(campos, "telefono")
	}
	if s.Correo != nil {
		campos = append(campos, "correo")
	}
	return strings.Join(campos, ",")
}

func (d *Deps) crearPaciente(r *http.Request) (int, any, error) {
	var s solicitudPaciente
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if err := s.validar(true); err != nil {
		return 0, nil, err
	}
	var out *paciente
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO paciente (clinica_id, identificacion, nombre, apellidos, fecha_nacimiento, telefono, correo)
			 VALUES (app_clinica_actual(), $1, $2, $3, $4::date, nullif($5, ''), nullif($6, '')) RETURNING id`,
			strings.TrimSpace(*s.Identificacion), strings.TrimSpace(*s.Nombre), strings.TrimSpace(*s.Apellidos),
			*s.FechaNacimiento, recortar(s.Telefono), recortar(s.Correo)).Scan(&id); err != nil {
			return err
		}
		var err error
		if out, err = una[paciente](ctx, tx, "Paciente no encontrado", sqlPaciente+` WHERE id = $1`, id); err != nil {
			return err
		}
		if out.tieneContacto() {
			return auditar(ctx, tx, accionEscritura, recursoContacto, id, nil, "alta: "+s.tocaContacto())
		}
		return nil
	})
	return http.StatusCreated, out, err
}

func (d *Deps) actualizarPaciente(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var s solicitudPaciente
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if err := s.validar(false); err != nil {
		return 0, nil, err
	}
	var out *paciente
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE paciente SET
			   identificacion   = coalesce($2, identificacion),
			   nombre           = coalesce($3, nombre),
			   apellidos        = coalesce($4, apellidos),
			   fecha_nacimiento = coalesce($5::date, fecha_nacimiento),
			   telefono         = CASE WHEN $6::text IS NULL THEN telefono ELSE nullif($6, '') END,
			   correo           = CASE WHEN $7::text IS NULL THEN correo   ELSE nullif($7, '') END,
			   actualizado_en   = now()
			 WHERE id = $1`,
			id, recortar(s.Identificacion), recortar(s.Nombre), recortar(s.Apellidos), s.FechaNacimiento,
			recortar(s.Telefono), recortar(s.Correo))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errores.NoEncontrado("Paciente no encontrado")
		}
		if out, err = una[paciente](ctx, tx, "Paciente no encontrado", sqlPaciente+` WHERE id = $1`, id); err != nil {
			return err
		}
		if campos := s.tocaContacto(); campos != "" {
			return auditar(ctx, tx, accionEscritura, recursoContacto, id, nil, "actualización: "+campos)
		}
		if out.tieneContacto() {
			return auditar(ctx, tx, accionLectura, recursoContacto, id, nil, "PATCH /pacientes/{id}")
		}
		return nil
	})
	return http.StatusOK, out, err
}

func (d *Deps) historialPaciente(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var out []citaDetalle
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		if err := existeID(ctx, tx, `SELECT EXISTS (SELECT 1 FROM paciente WHERE id = $1)`, id, "Paciente"); err != nil {
			return err
		}
		var err error
		out, err = lista[citaDetalle](ctx, tx, sqlCitaDetalle+` WHERE c.paciente_id = $1 ORDER BY c.inicio DESC`, id)
		return err
	})
	return http.StatusOK, out, err
}

type nota struct {
	ID            uuid.UUID `db:"id" json:"id"`
	CitaID        uuid.UUID `db:"cita_id" json:"cita_id"`
	ProfesionalID uuid.UUID `db:"profesional_id" json:"profesional_id"`
	PacienteID    uuid.UUID `db:"paciente_id" json:"paciente_id"`
	Contenido     string    `db:"contenido" json:"contenido"`
	CreadaEn      time.Time `db:"creada_en" json:"creada_en"`
}

const sqlNota = `SELECT n.id, n.cita_id, n.profesional_id, c.paciente_id, n.contenido, n.creada_en
	FROM nota_clinica n JOIN cita c ON c.id = n.cita_id`

func (d *Deps) escribirNota(r *http.Request) (int, any, error) {
	citaID, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var s struct {
		Contenido string `json:"contenido"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if err := requerido(map[string]string{"contenido": s.Contenido}); err != nil {
		return 0, nil, err
	}
	var out *nota
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var prof, paciente uuid.UUID
		err := tx.QueryRow(ctx, `SELECT profesional_id, paciente_id FROM cita WHERE id = $1`, citaID).Scan(&prof, &paciente)
		if errors.Is(err, pgx.ErrNoRows) {
			return errores.NoEncontrado("Cita no encontrada")
		}
		if err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO nota_clinica (clinica_id, cita_id, profesional_id, contenido)
			 VALUES (app_clinica_actual(), $1, $2, $3) RETURNING id`,
			citaID, prof, strings.TrimSpace(s.Contenido)).Scan(&id); err != nil {
			return err
		}
		if out, err = una[nota](ctx, tx, "Nota no encontrada", sqlNota+` WHERE n.id = $1`, id); err != nil {
			return err
		}
		return auditar(ctx, tx, accionEscritura, recursoNota, paciente, &id, "")
	})
	return http.StatusCreated, out, err
}

func (d *Deps) leerNota(r *http.Request) (int, any, error) {
	citaID, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var out *nota
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if out, err = una[nota](ctx, tx, "Nota no encontrada", sqlNota+` WHERE n.cita_id = $1`, citaID); err != nil {
			return err
		}
		return auditar(ctx, tx, accionLectura, recursoNota, out.PacienteID, &out.ID, "")
	})
	return http.StatusOK, out, err
}

type registroAuditoria struct {
	ID         int64         `db:"id" json:"id"`
	UsuarioID  uuid.UUID     `db:"usuario_id" json:"usuario_id"`
	Usuario    string        `db:"usuario" json:"usuario"`
	Accion     string        `db:"accion" json:"accion"`
	Recurso    string        `db:"recurso" json:"recurso"`
	PacienteID uuid.UUID     `db:"paciente_id" json:"paciente_id"`
	Paciente   string        `db:"paciente" json:"paciente"`
	RecursoID  uuid.NullUUID `db:"recurso_id" json:"recurso_id"`
	Detalle    *string       `db:"detalle" json:"detalle"`
	OcurridoEn time.Time     `db:"ocurrido_en" json:"ocurrido_en"`
}

func (d *Deps) consultarAuditoria(r *http.Request) (int, any, error) {
	usuario, err := QueryUUIDOpcional(r, "usuario_id")
	if err != nil {
		return 0, nil, err
	}
	paciente, err := QueryUUIDOpcional(r, "paciente_id")
	if err != nil {
		return 0, nil, err
	}
	limite, desplazamiento, err := paginacion(r)
	if err != nil {
		return 0, nil, err
	}
	var out []registroAuditoria
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
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
		out, err = lista[registroAuditoria](ctx, tx,
			`SELECT a.id, a.usuario_id, u.nombre_visible AS usuario, a.accion::text AS accion, a.recurso::text AS recurso,
			        a.paciente_id, p.nombre || ' ' || p.apellidos AS paciente, a.recurso_id, a.detalle, a.ocurrido_en
			   FROM auditoria a
			   JOIN usuario u  ON u.id = a.usuario_id
			   JOIN paciente p ON p.id = a.paciente_id
			  WHERE ($1::uuid IS NULL OR a.usuario_id = $1)
			    AND ($2::uuid IS NULL OR a.paciente_id = $2)
			    AND ($3::timestamptz IS NULL OR a.ocurrido_en >= $3)
			    AND ($4::timestamptz IS NULL OR a.ocurrido_en < $4)
			  ORDER BY a.ocurrido_en DESC, a.id DESC
			  LIMIT $5 OFFSET $6`, usuario, paciente, desde, hasta, limite, desplazamiento)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) citasPorEstado(r *http.Request) (int, any, error) {
	if r.URL.Query().Get("desde") == "" || r.URL.Query().Get("hasta") == "" {
		return 0, nil, errores.Invalido("Faltan desde y hasta")
	}
	out := map[string]int{"reservada": 0, "atendida": 0, "ausente": 0, "cancelada": 0, "reprogramada": 0}
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
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
		if !hasta.After(*desde) {
			return errores.Invalido("hasta tiene que ser posterior o igual a desde")
		}
		filas, err := tx.Query(ctx,
			`SELECT estado::text, count(*) FROM cita WHERE inicio >= $1 AND inicio < $2 GROUP BY estado`, desde, hasta)
		if err != nil {
			return err
		}
		defer filas.Close()
		for filas.Next() {
			var estado string
			var n int
			if err := filas.Scan(&estado, &n); err != nil {
				return err
			}
			out[estado] = n
		}
		return filas.Err()
	})
	return http.StatusOK, out, err
}
