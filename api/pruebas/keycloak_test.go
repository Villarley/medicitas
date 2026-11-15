package pruebas

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"medicitas/api/internal/keycloak"
)

func TestKeycloakAdmin(t *testing.T) {
	ctx := context.Background()
	adm := keycloak.NuevoAdmin(keycloakURL, "medicitas", env("KEYCLOAK_API_CLIENT_ID", "medicitas-api"),
		env("KEYCLOAK_API_CLIENT_SECRET", "medicitas-api-secret-dev"))
	username := "prueba." + uuid.NewString()[:8]
	id, err := adm.CrearUsuario(ctx, keycloak.NuevoUsuario{Username: username, Nombre: "Prueba", Apellidos: "Keycloak",
		Correo: username + "@medicitas.test", Clave: "Clave.Prueba1", Rol: "recepcion"})
	require.NoError(t, err)
	defer adm.BorrarUsuario(ctx, id) //nolint:errcheck

	pedirToken := func() int {
		resp, err := clienteHTTP.PostForm(keycloakURL+"/realms/medicitas/protocol/openid-connect/token", url.Values{
			"grant_type": {"password"}, "client_id": {"medicitas-cli"}, "username": {username}, "password": {"Clave.Prueba1"}})
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, http.StatusOK, pedirToken())

	_, err = adm.CrearUsuario(ctx, keycloak.NuevoUsuario{Username: username, Nombre: "X", Apellidos: "Y",
		Correo: "otro" + username + "@medicitas.test", Clave: "Clave.Prueba1", Rol: "recepcion"})
	require.ErrorIs(t, err, keycloak.ErrUsuarioExiste)

	require.NoError(t, adm.CambiarRol(ctx, id, "recepcion", "profesional"))
	require.NoError(t, adm.Deshabilitar(ctx, id))
	require.NotEqual(t, http.StatusOK, pedirToken(), "un usuario dado de baja no obtiene token")
}
