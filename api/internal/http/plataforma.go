package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
	"medicitas/api/internal/keycloak"
)

// El alta de clínicas y de usuarios toca dos sistemas: Keycloak (credenciales y
// rol del token) y la base (usuario.idp_sub, clínica y rol). No hay transacción
// distribuida, así que el orden es: primero Keycloak, después la base, y si la
// base falla se borra el usuario recién creado en Keycloak. Si aun así quedara
// un usuario de Keycloak sin fila, no entra: el middleware exige la fila (403).

type nuevoUsuarioIdP struct {
	Username  string `json:"username"`
	Nombre    string `json:"nombre"`
	Apellidos string `json:"apellidos"`
	Correo    string `json:"correo"`
	Clave     string `json:"clave"`
}

var reUsername = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,59}$`)

func (u *nuevoUsuarioIdP) validar() error {
	if err := requerido(map[string]string{"username": u.Username, "nombre": u.Nombre,
		"apellidos": u.Apellidos, "correo": u.Correo, "clave": u.Clave}); err != nil {
		return err
	}
	u.Username = strings.ToLower(strings.TrimSpace(u.Username))
	if !reUsername.MatchString(u.Username) {
		return errores.Invalido("username: 3 a 60 caracteres, minúsculas, números, punto, guion o guion bajo")
	}
	if _, err := mail.ParseAddress(u.Correo); err != nil {
		return errores.Invalido("correo inválido")
	}
	if len(u.Clave) < 8 {
		return errores.Invalido("La clave debe tener al menos 8 caracteres")
	}
	return nil
}

func (u *nuevoUsuarioIdP) nombreVisible() string {
	return strings.TrimSpace(u.Nombre) + " " + strings.TrimSpace(u.Apellidos)
}

func (d *Deps) crearEnIdP(ctx context.Context, u nuevoUsuarioIdP, rol string) (uuid.UUID, error) {
	id, err := d.Keycloak.CrearUsuario(ctx, keycloak.NuevoUsuario{Username: u.Username, Nombre: strings.TrimSpace(u.Nombre),
		Apellidos: strings.TrimSpace(u.Apellidos), Correo: strings.TrimSpace(u.Correo), Clave: u.Clave, Rol: rol})
	if errors.Is(err, keycloak.ErrUsuarioExiste) {
		return uuid.Nil, errores.Conflicto("DUPLICADO", "Ese username o correo ya existe en el proveedor de identidad")
	}
	if err != nil {
		slog.Error("keycloak no creó el usuario", "error", err)
		return uuid.Nil, errores.Nuevo(http.StatusBadGateway, "PROVEEDOR_IDENTIDAD", "El proveedor de identidad no respondió como se esperaba")
	}
	sub, err := uuid.Parse(id)
	if err != nil {
		_ = d.Keycloak.BorrarUsuario(ctx, id)
		return uuid.Nil, errores.Nuevo(http.StatusBadGateway, "PROVEEDOR_IDENTIDAD", "Identificador inesperado del proveedor")
	}
	return sub, nil
}

func (d *Deps) compensar(sub uuid.UUID) {
	// Contexto propio: la petición pudo haberse cancelado y la compensación igual tiene que correr.
	ctx, cancelar := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelar()
	if err := d.Keycloak.BorrarUsuario(ctx, sub.String()); err != nil {
		slog.Error("compensación fallida: usuario huérfano en keycloak", "sub", sub, "error", err)
	}
}

type clinica struct {
	ID          uuid.UUID `db:"id" json:"id"`
	Codigo      string    `db:"codigo" json:"codigo"`
	Nombre      string    `db:"nombre" json:"nombre"`
	ZonaHoraria string    `db:"zona_horaria" json:"zona_horaria"`
	Activa      bool      `db:"activa" json:"activa"`
	CreadaEn    time.Time `db:"creada_en" json:"creada_en"`
}

const sqlClinica = `SELECT id, codigo, nombre, zona_horaria, activa, creada_en FROM clinica`

type usuarioFila struct {
	ID            uuid.UUID  `db:"id" json:"id"`
	Rol           string     `db:"rol_codigo" json:"rol"`
	NombreVisible string     `db:"nombre_visible" json:"nombre_visible"`
	Activo        bool       `db:"activo" json:"activo"`
	CreadoEn      time.Time  `db:"creado_en" json:"creado_en"`
	BajaEn        *time.Time `db:"baja_en" json:"baja_en"`
}

const sqlUsuario = `SELECT id, rol_codigo, nombre_visible, activo, creado_en, baja_en FROM usuario`

var reCodigoClinica = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

func (d *Deps) listarClinicas(r *http.Request) (int, any, error) {
	var out []clinica
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = lista[clinica](ctx, tx, sqlClinica+` ORDER BY nombre`)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) crearClinica(r *http.Request) (int, any, error) {
	var s struct {
		Codigo      string          `json:"codigo"`
		Nombre      string          `json:"nombre"`
		ZonaHoraria string          `json:"zona_horaria"`
		Admin       nuevoUsuarioIdP `json:"admin"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if err := requerido(map[string]string{"codigo": s.Codigo, "nombre": s.Nombre, "zona_horaria": s.ZonaHoraria}); err != nil {
		return 0, nil, err
	}
	s.Codigo = strings.TrimSpace(s.Codigo)
	if !reCodigoClinica.MatchString(s.Codigo) {
		return 0, nil, errores.Invalido("codigo: 3 a 40 caracteres en minúscula, números o guion")
	}
	if _, err := time.LoadLocation(s.ZonaHoraria); err != nil || s.ZonaHoraria == "Local" {
		return 0, nil, errores.Invalido("zona_horaria inválida (por ejemplo America/Costa_Rica)")
	}
	if err := s.Admin.validar(); err != nil {
		return 0, nil, err
	}

	// Antes de tocar Keycloak se descarta el caso común de código repetido.
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var existe bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clinica WHERE codigo = $1)`, s.Codigo).Scan(&existe); err != nil {
			return err
		}
		if existe {
			return errores.Conflicto("DUPLICADO", "Ya existe una clínica con ese código")
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}

	sub, err := d.crearEnIdP(r.Context(), s.Admin, identidad.AdminClinica)
	if err != nil {
		return 0, nil, err
	}
	var out struct {
		Clinica *clinica     `json:"clinica"`
		Admin   *usuarioFila `json:"admin"`
	}
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if out.Clinica, err = una[clinica](ctx, tx, "",
			`INSERT INTO clinica (codigo, nombre, zona_horaria) VALUES ($1, $2, $3)
			 RETURNING id, codigo, nombre, zona_horaria, activa, creada_en`,
			s.Codigo, strings.TrimSpace(s.Nombre), s.ZonaHoraria); err != nil {
			return err
		}
		out.Admin, err = una[usuarioFila](ctx, tx, "",
			`INSERT INTO usuario (clinica_id, idp_sub, rol_codigo, nombre_visible) VALUES ($1, $2, 'admin_clinica', $3)
			 RETURNING id, rol_codigo, nombre_visible, activo, creado_en, baja_en`,
			out.Clinica.ID, sub, s.Admin.nombreVisible())
		return err
	})
	if err != nil {
		d.compensar(sub)
		return 0, nil, err
	}
	return http.StatusCreated, out, nil
}

var rolesAsignables = map[string]bool{identidad.AdminClinica: true, identidad.Recepcion: true, identidad.Profesional: true}

func (d *Deps) listarUsuarios(r *http.Request) (int, any, error) {
	filtro := r.URL.Query().Get("activo")
	if filtro != "" && filtro != "true" && filtro != "false" {
		return 0, nil, errores.Invalido("activo debe ser true o false")
	}
	var out []usuarioFila
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = lista[usuarioFila](ctx, tx,
			sqlUsuario+` WHERE ($1 = '' OR activo = ($1 = 'true')) ORDER BY nombre_visible`, filtro)
		return err
	})
	return http.StatusOK, out, err
}

func (d *Deps) crearUsuario(r *http.Request) (int, any, error) {
	var s struct {
		nuevoUsuarioIdP
		Rol string `json:"rol"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if !rolesAsignables[s.Rol] {
		return 0, nil, errores.Invalido("rol debe ser admin_clinica, recepcion o profesional")
	}
	if err := s.validar(); err != nil {
		return 0, nil, err
	}
	sub, err := d.crearEnIdP(r.Context(), s.nuevoUsuarioIdP, s.Rol)
	if err != nil {
		return 0, nil, err
	}
	var out *usuarioFila
	err = d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = una[usuarioFila](ctx, tx, "",
			`INSERT INTO usuario (clinica_id, idp_sub, rol_codigo, nombre_visible)
			 VALUES (app_clinica_actual(), $1, $2, $3)
			 RETURNING id, rol_codigo, nombre_visible, activo, creado_en, baja_en`,
			sub, s.Rol, s.nombreVisible())
		return err
	})
	if err != nil {
		d.compensar(sub)
		return 0, nil, err
	}
	return http.StatusCreated, out, nil
}

func (d *Deps) cambioDeUsuario(r *http.Request, id uuid.UUID,
	fn func(ctx context.Context, tx pgx.Tx, actual usuarioFila, sub uuid.UUID) error) (*usuarioFila, error) {
	if id == Ident(r).UsuarioID {
		return nil, errores.Conflicto("AUTOGESTION", "No podés cambiar tu propio rol ni darte de baja")
	}
	var out *usuarioFila
	err := d.enTx(r, func(ctx context.Context, tx pgx.Tx) error {
		var actual usuarioFila
		var sub uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT id, rol_codigo, nombre_visible, activo, creado_en, baja_en, idp_sub FROM usuario WHERE id = $1 FOR UPDATE`, id,
		).Scan(&actual.ID, &actual.Rol, &actual.NombreVisible, &actual.Activo, &actual.CreadoEn, &actual.BajaEn, &sub)
		if errors.Is(err, pgx.ErrNoRows) {
			return errores.NoEncontrado("Usuario no encontrado")
		}
		if err != nil {
			return err
		}
		if err := fn(ctx, tx, actual, sub); err != nil {
			return err
		}
		out, err = una[usuarioFila](ctx, tx, "Usuario no encontrado", sqlUsuario+` WHERE id = $1`, id)
		return err
	})
	return out, err
}

func errorIdP(err error) error {
	slog.Error("keycloak rechazó el cambio", "error", err)
	return errores.Nuevo(http.StatusBadGateway, "PROVEEDOR_IDENTIDAD", "El proveedor de identidad no aplicó el cambio")
}

func (d *Deps) cambiarRolUsuario(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	var s struct {
		Rol string `json:"rol"`
	}
	if err := LeerJSON(r, &s); err != nil {
		return 0, nil, err
	}
	if !rolesAsignables[s.Rol] {
		return 0, nil, errores.Invalido("rol debe ser admin_clinica, recepcion o profesional")
	}
	out, err := d.cambioDeUsuario(r, id, func(ctx context.Context, tx pgx.Tx, actual usuarioFila, sub uuid.UUID) error {
		if !actual.Activo {
			return errores.Conflicto("USUARIO_INACTIVO", "El usuario está dado de baja")
		}
		if actual.Rol == s.Rol {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE usuario SET rol_codigo = $2 WHERE id = $1`, id, s.Rol); err != nil {
			return err
		}
		if err := d.Keycloak.CambiarRol(ctx, sub.String(), actual.Rol, s.Rol); err != nil {
			return errorIdP(err)
		}
		return nil
	})
	return http.StatusOK, out, err
}

func (d *Deps) bajaUsuario(r *http.Request) (int, any, error) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	_, err = d.cambioDeUsuario(r, id, func(ctx context.Context, tx pgx.Tx, actual usuarioFila, sub uuid.UUID) error {
		if !actual.Activo {
			return nil // idempotente
		}
		if _, err := tx.Exec(ctx, `UPDATE usuario SET activo = false, baja_en = now() WHERE id = $1`, id); err != nil {
			return err
		}
		if err := d.Keycloak.Deshabilitar(ctx, sub.String()); err != nil {
			return errorIdP(err)
		}
		return nil
	})
	return http.StatusNoContent, nil, err
}
