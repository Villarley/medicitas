// Package pruebas tiene las pruebas de integración: hablan con la pila real
// (API, PostgreSQL, Keycloak y Redis) que levanta docker compose.
//
// Se corren dentro del contenedor de la API:
//
//	docker compose exec api go test -count=1 ./...
//
// Cada prueba arma su propio escenario (profesional, servicios y pacientes nuevos
// en una fecha lejana), así se pueden correr muchas veces sin limpiar la base.
package pruebas

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func env(nombre, porDefecto string) string {
	if v := os.Getenv(nombre); v != "" {
		return v
	}
	return porDefecto
}

var (
	apiURL      = env("API_URL", "http://localhost:8000")
	keycloakURL = env("KEYCLOAK_URL_INTERNA", "http://keycloak:8080")
	clavePrueba = env("CLAVE_USUARIOS_PRUEBA", "Medicitas.2026")
	databaseURL = env("DATABASE_URL", "postgresql://medicitas_app:app-dev@db:5432/medicitas")

	clienteHTTP = &http.Client{Timeout: 20 * time.Second}
	tokens      sync.Map
	zonaCR, _   = time.LoadLocation("America/Costa_Rica")
)

// Token pide (y recuerda) un token por password grant con el client de pruebas.
func Token(t *testing.T, usuario string) string {
	t.Helper()
	if v, ok := tokens.Load(usuario); ok {
		return v.(string)
	}
	form := url.Values{"grant_type": {"password"}, "client_id": {"medicitas-cli"},
		"username": {usuario}, "password": {clavePrueba}}
	resp, err := clienteHTTP.PostForm(keycloakURL+"/realms/medicitas/protocol/openid-connect/token", form)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "no se pudo obtener token para %s", usuario)
	var cuerpo struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&cuerpo))
	tokens.Store(usuario, cuerpo.AccessToken)
	return cuerpo.AccessToken
}

// Respuesta de la API ya leída.
type Respuesta struct {
	Status int
	Cuerpo []byte
}

func (r Respuesta) JSON(t *testing.T, destino any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(r.Cuerpo, destino), string(r.Cuerpo))
}

// Codigo devuelve error.codigo de una respuesta de error.
func (r Respuesta) Codigo() string {
	var e struct {
		Error struct {
			Codigo string `json:"codigo"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Cuerpo, &e)
	return e.Error.Codigo
}

// Pedir hace una petición a la API. cuerpo puede ser nil, un string (se manda tal cual) o cualquier valor JSON.
func Pedir(t *testing.T, metodo, ruta, token string, cuerpo any) Respuesta {
	t.Helper()
	var r io.Reader
	switch c := cuerpo.(type) {
	case nil:
	case string:
		r = strings.NewReader(c)
	default:
		b, err := json.Marshal(c)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(metodo, apiURL+"/api/v1"+ruta, r)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := clienteHTTP.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return Respuesta{Status: resp.StatusCode, Cuerpo: b}
}

// subDelToken lee el sub del payload sin verificar (solo para armar datos de prueba).
func subDelToken(t *testing.T, token string) string {
	partes := strings.Split(token, ".")
	require.Len(t, partes, 3)
	b, err := base64.RawURLEncoding.DecodeString(partes[1])
	require.NoError(t, err)
	var c struct {
		Sub string `json:"sub"`
	}
	require.NoError(t, json.Unmarshal(b, &c))
	return c.Sub
}

// ComoAdmin corre fn dentro de una transacción directa a la base con el perfil
// db_admin_clinica de la clínica dada (mismo mecanismo que usa la API).
// Se usa para preparar datos que todavía no tienen ruta propia.
func ComoAdmin(t *testing.T, usuarioAdmin string, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	defer conn.Close(ctx)
	var usuarioID, clinicaID uuid.UUID
	require.NoError(t, conn.QueryRow(ctx, `SELECT usuario_id, clinica_id FROM resolver_identidad($1)`,
		subDelToken(t, Token(t, usuarioAdmin))).Scan(&usuarioID, &clinicaID))
	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, "SET LOCAL ROLE db_admin_clinica")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.clinica_id', $1, true), set_config('app.usuario_id', $2, true)`,
		clinicaID.String(), usuarioID.String())
	require.NoError(t, err)
	require.NoError(t, fn(ctx, tx))
	require.NoError(t, tx.Commit(ctx))
}

// Escenario: datos propios de una prueba en Clínica Central Cartago.
type Escenario struct {
	Prof, Prof2      uuid.UUID // atienden lunes 08:00-12:00 y 13:00-17:00
	Serv30, Serv45   uuid.UUID
	Pacientes        []uuid.UUID
	Lunes            time.Time // un lunes lejano, en hora de Costa Rica
	recepcion, admin string
}

const (
	usuarioRecepcion  = "recepcion.central-cartago"
	usuarioRecepcion2 = "recepcion2.central-cartago"
	usuarioAdmin      = "admin.central-cartago"
)

// NuevoEscenario crea dos profesionales, dos servicios (30 y 45 min) y cinco pacientes nuevos.
func NuevoEscenario(t *testing.T) *Escenario {
	t.Helper()
	sufijo := uuid.NewString()[:8]
	hoy := time.Now().In(zonaCR)
	dias := 200 + rand.IntN(700)
	base := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, zonaCR).AddDate(0, 0, dias)
	for base.Weekday() != time.Monday {
		base = base.AddDate(0, 0, 1)
	}
	e := &Escenario{Lunes: base, recepcion: usuarioRecepcion, admin: usuarioAdmin}
	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		var esp uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM especialidad ORDER BY nombre LIMIT 1`).Scan(&esp); err != nil {
			return err
		}
		for _, s := range []struct {
			dest *uuid.UUID
			min  int
		}{{&e.Serv30, 30}, {&e.Serv45, 45}} {
			if err := tx.QueryRow(ctx,
				`INSERT INTO servicio (clinica_id, nombre, duracion_min, especialidad_id)
				 VALUES (app_clinica_actual(), $1, $2, $3) RETURNING id`,
				fmt.Sprintf("Prueba %d min %s", s.min, sufijo), s.min, esp).Scan(s.dest); err != nil {
				return err
			}
		}
		for i, dest := range []*uuid.UUID{&e.Prof, &e.Prof2} {
			if err := tx.QueryRow(ctx,
				`INSERT INTO profesional (clinica_id, nombre, especialidad_id)
				 VALUES (app_clinica_actual(), $1, $2) RETURNING id`,
				fmt.Sprintf("Dr. Prueba %d %s", i+1, sufijo), esp).Scan(dest); err != nil {
				return err
			}
			for _, s := range []uuid.UUID{e.Serv30, e.Serv45} {
				if _, err := tx.Exec(ctx,
					`INSERT INTO profesional_servicio (clinica_id, profesional_id, servicio_id) VALUES (app_clinica_actual(), $1, $2)`,
					*dest, s); err != nil {
					return err
				}
			}
			for _, b := range [][2]string{{"08:00", "12:00"}, {"13:00", "17:00"}} {
				if _, err := tx.Exec(ctx,
					`INSERT INTO horario_atencion (clinica_id, profesional_id, dia_semana, hora_inicio, hora_fin)
					 VALUES (app_clinica_actual(), $1, 1, $2::time, $3::time)`, *dest, b[0], b[1]); err != nil {
					return err
				}
			}
		}
		for i := 0; i < 5; i++ {
			var p uuid.UUID
			if err := tx.QueryRow(ctx,
				`INSERT INTO paciente (clinica_id, identificacion, nombre, apellidos, fecha_nacimiento, telefono)
				 VALUES (app_clinica_actual(), $1, 'Paciente', $2, '1990-05-10', '88880000') RETURNING id`,
				fmt.Sprintf("P-%s-%d", sufijo, i), "Prueba "+sufijo).Scan(&p); err != nil {
				return err
			}
			e.Pacientes = append(e.Pacientes, p)
		}
		return nil
	})
	return e
}

// Hora devuelve el lunes del escenario más `semanas`, a la hora local dada.
func (e *Escenario) Hora(semanas, hora, minuto int) time.Time {
	d := e.Lunes.AddDate(0, 0, 7*semanas)
	return time.Date(d.Year(), d.Month(), d.Day(), hora, minuto, 0, 0, zonaCR)
}

func (e *Escenario) Fecha(semanas int) string {
	return e.Lunes.AddDate(0, 0, 7*semanas).Format("2006-01-02")
}

// Reservar es un atajo para POST /citas.
func (e *Escenario) Reservar(t *testing.T, token string, paciente, prof, servicio uuid.UUID, inicio time.Time) Respuesta {
	return Pedir(t, http.MethodPost, "/citas", token, map[string]any{
		"paciente_id": paciente, "profesional_id": prof, "servicio_id": servicio, "inicio": inicio.Format(time.RFC3339),
	})
}

// ContarVigentesQueCruzan cuenta en la base las citas reservadas del profesional que se cruzan con [ini, fin).
func ContarVigentesQueCruzan(t *testing.T, prof uuid.UUID, ini, fin time.Time) int {
	t.Helper()
	var n int
	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM cita WHERE profesional_id = $1 AND estado = 'reservada'
			   AND tstzrange(inicio, fin, '[)') && tstzrange($2, $3, '[)')`, prof, ini, fin).Scan(&n)
	})
	return n
}

type citaJSON struct {
	ID            uuid.UUID     `json:"id"`
	ClinicaID     uuid.UUID     `json:"clinica_id"`
	ProfesionalID uuid.UUID     `json:"profesional_id"`
	Inicio        time.Time     `json:"inicio"`
	Fin           time.Time     `json:"fin"`
	Estado        string        `json:"estado"`
	CitaOrigenID  uuid.NullUUID `json:"cita_origen_id"`
}

type consulta func(sql string, destinos ...any)

// ComoAdminDe es como ComoAdmin pero con una función de consulta simple de una fila.
func ComoAdminDe(t *testing.T, usuario string, fn func(q consulta)) {
	t.Helper()
	ComoAdmin(t, usuario, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		fn(func(sql string, destinos ...any) {
			if err == nil {
				err = tx.QueryRow(ctx, sql).Scan(destinos...)
			}
		})
		return err
	})
}
