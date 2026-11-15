package pruebas

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestAltaDeClinica(t *testing.T) {
	tokPlat := Token(t, "plataforma")
	sufijo := uuid.NewString()[:8]
	admin := "admin.nueva-" + sufijo
	cuerpo := map[string]any{
		"codigo": "nueva-" + sufijo, "nombre": "Clínica Nueva " + sufijo, "zona_horaria": "America/Costa_Rica",
		"admin": map[string]any{"username": admin, "nombre": "Ada", "apellidos": "Nueva",
			"correo": admin + "@medicitas.test", "clave": clavePrueba},
	}

	// Solo la plataforma.
	r := Pedir(t, http.MethodPost, "/plataforma/clinicas", Token(t, usuarioAdmin), cuerpo)
	require.Equal(t, http.StatusForbidden, r.Status, "un admin de clínica no da de alta clínicas")

	r = Pedir(t, http.MethodPost, "/plataforma/clinicas", tokPlat, cuerpo)
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var alta struct {
		Clinica struct {
			ID     uuid.UUID `json:"id"`
			Codigo string    `json:"codigo"`
		} `json:"clinica"`
		Admin struct {
			Rol string `json:"rol"`
		} `json:"admin"`
	}
	r.JSON(t, &alta)
	require.Equal(t, "admin_clinica", alta.Admin.Rol)

	require.Contains(t, IDs(t, Pedir(t, http.MethodGet, "/plataforma/clinicas", tokPlat, nil)), alta.Clinica.ID)

	// Código repetido: 409, y no queda un usuario suelto en Keycloak.
	otra := map[string]any{"codigo": "nueva-" + sufijo, "nombre": "Otra", "zona_horaria": "America/Costa_Rica",
		"admin": map[string]any{"username": "otro." + sufijo, "nombre": "X", "apellidos": "Y",
			"correo": "otro." + sufijo + "@medicitas.test", "clave": clavePrueba}}
	r = Pedir(t, http.MethodPost, "/plataforma/clinicas", tokPlat, otra)
	require.Equal(t, http.StatusConflict, r.Status, string(r.Cuerpo))
	require.Equal(t, "DUPLICADO", r.Codigo())

	// Zona horaria inventada: 422 antes de tocar Keycloak.
	otra["codigo"], otra["zona_horaria"] = "otra-"+sufijo, "America/Atlantida"
	r = Pedir(t, http.MethodPost, "/plataforma/clinicas", tokPlat, otra)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, string(r.Cuerpo))

	// El admin nuevo entra y su clínica está vacía: nada de la semilla de las otras.
	tokNuevo := Token(t, admin)
	require.Empty(t, IDs(t, Pedir(t, http.MethodGet, "/pacientes", tokNuevo, nil)))
	require.Empty(t, IDs(t, Pedir(t, http.MethodGet, "/servicios", tokNuevo, nil)))
	require.Empty(t, IDs(t, Pedir(t, http.MethodGet, "/profesionales", tokNuevo, nil)))
	usuarios := IDs(t, Pedir(t, http.MethodGet, "/usuarios", tokNuevo, nil))
	require.Len(t, usuarios, 1, "solo él mismo")

	// La plataforma no accede a datos clínicos.
	for _, ruta := range []string{"/pacientes", "/servicios", "/usuarios", "/auditoria", "/agenda?fecha=2030-01-07"} {
		r = Pedir(t, http.MethodGet, ruta, tokPlat, nil)
		require.Equal(t, http.StatusForbidden, r.Status, ruta)
	}
}

func TestGestionDeUsuarios(t *testing.T) {
	tokAdmin := Token(t, usuarioAdmin)
	sufijo := uuid.NewString()[:8]
	username := "recep.prueba." + sufijo
	cuerpo := map[string]any{"username": username, "nombre": "Rita", "apellidos": "Prueba",
		"correo": username + "@medicitas.test", "clave": clavePrueba, "rol": "recepcion"}

	r := Pedir(t, http.MethodPost, "/usuarios", Token(t, usuarioRecepcion), cuerpo)
	require.Equal(t, http.StatusForbidden, r.Status, "recepción no gestiona usuarios")

	malo := map[string]any{}
	for k, v := range cuerpo {
		malo[k] = v
	}
	malo["rol"] = "admin_plataforma"
	r = Pedir(t, http.MethodPost, "/usuarios", tokAdmin, malo)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, "un admin de clínica no crea admins de plataforma")

	r = Pedir(t, http.MethodPost, "/usuarios", tokAdmin, cuerpo)
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var u struct {
		ID  uuid.UUID `json:"id"`
		Rol string    `json:"rol"`
	}
	r.JSON(t, &u)
	require.Equal(t, "recepcion", u.Rol)

	r = Pedir(t, http.MethodPost, "/usuarios", tokAdmin, cuerpo)
	require.Equal(t, http.StatusConflict, r.Status, "username repetido")

	// El usuario nuevo entra con el rol asignado.
	tokNuevo := Token(t, username)
	require.Equal(t, http.StatusOK, Pedir(t, http.MethodGet, "/pacientes", tokNuevo, nil).Status)

	// Cambio de rol: el token viejo dice "recepcion" y la base ya dice "profesional": 403.
	r = Pedir(t, http.MethodPatch, "/usuarios/"+u.ID.String(), tokAdmin, map[string]any{"rol": "profesional"})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	r.JSON(t, &u)
	require.Equal(t, "profesional", u.Rol)
	require.Equal(t, http.StatusForbidden, Pedir(t, http.MethodGet, "/pacientes", tokNuevo, nil).Status)
	tokens.Delete(username)
	tokNuevo = Token(t, username)
	require.Equal(t, http.StatusOK, Pedir(t, http.MethodGet, "/pacientes", tokNuevo, nil).Status, "token nuevo con el rol nuevo")

	// Nadie se cambia ni se da de baja a sí mismo.
	r = Pedir(t, http.MethodDelete, "/usuarios/"+MiUsuario(t, usuarioAdmin).String(), tokAdmin, nil)
	require.Equal(t, http.StatusConflict, r.Status)

	// Baja lógica: 204, el token vigente deja de servir y Keycloak no emite otro.
	r = Pedir(t, http.MethodDelete, "/usuarios/"+u.ID.String(), tokAdmin, nil)
	require.Equal(t, http.StatusNoContent, r.Status, string(r.Cuerpo))
	require.Equal(t, http.StatusForbidden, Pedir(t, http.MethodGet, "/pacientes", tokNuevo, nil).Status)
	resp, err := clienteHTTP.PostForm(keycloakURL+"/realms/medicitas/protocol/openid-connect/token", url.Values{
		"grant_type": {"password"}, "client_id": {"medicitas-cli"}, "username": {username}, "password": {clavePrueba}})
	require.NoError(t, err)
	resp.Body.Close()
	require.NotEqual(t, http.StatusOK, resp.StatusCode)

	// La fila sigue (la referencian citas y bitácora), marcada inactiva.
	r = Pedir(t, http.MethodGet, "/usuarios?activo=false", tokAdmin, nil)
	require.Contains(t, IDs(t, r), u.ID)
	r = Pedir(t, http.MethodDelete, "/usuarios/"+u.ID.String(), tokAdmin, nil)
	require.Equal(t, http.StatusNoContent, r.Status, "la baja es idempotente")
	r = Pedir(t, http.MethodDelete, "/usuarios/"+uuid.NewString(), tokAdmin, nil)
	require.Equal(t, http.StatusNotFound, r.Status)
}

func MiUsuario(t *testing.T, usuario string) uuid.UUID {
	t.Helper()
	sub := subDelToken(t, Token(t, usuario))
	var id uuid.UUID
	ComoAdmin(t, usuarioAdmin, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM usuario WHERE idp_sub = $1`, sub).Scan(&id)
	})
	return id
}
