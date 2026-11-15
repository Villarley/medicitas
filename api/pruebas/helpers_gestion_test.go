package pruebas

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// Ayudas compartidas por las pruebas de catálogo, agenda, pacientes y plataforma.

const usuarioProf1 = "prof1.central-cartago"

func MiProfesional(t *testing.T, usuario string) uuid.UUID {
	t.Helper()
	sub := subDelToken(t, Token(t, usuario))
	var id uuid.UUID
	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT p.id FROM profesional p JOIN usuario u ON u.id = p.usuario_id WHERE u.idp_sub = $1`, sub).Scan(&id)
	})
	return id
}

func IDs(t *testing.T, r Respuesta) []uuid.UUID {
	t.Helper()
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	var filas []struct {
		ID uuid.UUID `json:"id"`
	}
	r.JSON(t, &filas)
	out := make([]uuid.UUID, len(filas))
	for i, f := range filas {
		out[i] = f.ID
	}
	return out
}

type ProfConUsuario struct {
	Username string
	UsuarioID,
	Prof, Servicio uuid.UUID
}

func NuevoProfConUsuario(t *testing.T) *ProfConUsuario {
	t.Helper()
	tokAdmin := Token(t, usuarioAdmin)
	sufijo := uuid.NewString()[:8]
	p := &ProfConUsuario{Username: "prof.prueba." + sufijo}

	r := Pedir(t, http.MethodPost, "/usuarios", tokAdmin, map[string]any{
		"username": p.Username, "nombre": "Prof", "apellidos": "Prueba " + sufijo,
		"correo": p.Username + "@medicitas.test", "clave": clavePrueba, "rol": "profesional"})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var u struct {
		ID uuid.UUID `json:"id"`
	}
	r.JSON(t, &u)
	p.UsuarioID = u.ID

	r = Pedir(t, http.MethodPost, "/especialidades", tokAdmin, map[string]any{"nombre": "Especialidad " + sufijo})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var esp struct {
		ID uuid.UUID `json:"id"`
	}
	r.JSON(t, &esp)

	r = Pedir(t, http.MethodPost, "/servicios", tokAdmin, map[string]any{
		"nombre": "Servicio " + sufijo, "duracion_min": 30, "especialidad_id": esp.ID})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var srv struct {
		ID uuid.UUID `json:"id"`
	}
	r.JSON(t, &srv)
	p.Servicio = srv.ID

	r = Pedir(t, http.MethodPost, "/profesionales", tokAdmin, map[string]any{
		"nombre": "Dra. Prueba " + sufijo, "especialidad_id": esp.ID, "usuario_id": p.UsuarioID})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var prof struct {
		ID uuid.UUID `json:"id"`
	}
	r.JSON(t, &prof)
	p.Prof = prof.ID

	r = Pedir(t, http.MethodPut, "/profesionales/"+p.Prof.String()+"/servicios", tokAdmin,
		map[string]any{"servicio_ids": []uuid.UUID{p.Servicio}})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))

	for dia := 1; dia <= 7; dia++ {
		r = Pedir(t, http.MethodPost, "/profesionales/"+p.Prof.String()+"/horarios", tokAdmin,
			map[string]any{"dia_semana": dia, "hora_inicio": "07:00", "hora_fin": "19:00"})
		require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	}
	return p
}

func (p *ProfConUsuario) CitaPasada(t *testing.T, paciente uuid.UUID) uuid.UUID {
	t.Helper()
	ayer := time.Now().In(zonaCR).AddDate(0, 0, -1)
	// Minutos al azar para que corridas repetidas no choquen en el mismo espacio.
	inicio := time.Date(ayer.Year(), ayer.Month(), ayer.Day(), 8, 0, 0, 0, zonaCR).
		Add(time.Duration(30*(int(uuid.New()[0])%20)) * time.Minute)
	var id uuid.UUID
	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO cita (clinica_id, paciente_id, profesional_id, servicio_id, inicio, fin, creada_por)
			 VALUES (app_clinica_actual(), $1, $2, $3, $4, $4, app_usuario_actual()) RETURNING id`,
			paciente, p.Prof, p.Servicio, inicio).Scan(&id)
	})
	return id
}

func NuevoPaciente(t *testing.T, token string, conContacto bool) uuid.UUID {
	t.Helper()
	cuerpo := map[string]any{"identificacion": "T-" + uuid.NewString()[:13], "nombre": "Ana",
		"apellidos": fmt.Sprintf("Prueba %s", uuid.NewString()[:6]), "fecha_nacimiento": "1985-02-14"}
	if conContacto {
		cuerpo["telefono"] = "8888-1234"
		cuerpo["correo"] = "ana.prueba@medicitas.test"
	}
	r := Pedir(t, http.MethodPost, "/pacientes", token, cuerpo)
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var p struct {
		ID uuid.UUID `json:"id"`
	}
	r.JSON(t, &p)
	return p.ID
}

type auditoriaJSON struct {
	UsuarioID  uuid.UUID     `json:"usuario_id"`
	Accion     string        `json:"accion"`
	Recurso    string        `json:"recurso"`
	PacienteID uuid.UUID     `json:"paciente_id"`
	RecursoID  uuid.NullUUID `json:"recurso_id"`
}

func Bitacora(t *testing.T, filtros string) []auditoriaJSON {
	t.Helper()
	r := Pedir(t, http.MethodGet, "/auditoria?"+filtros, Token(t, usuarioAdmin), nil)
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	var out []auditoriaJSON
	r.JSON(t, &out)
	return out
}
