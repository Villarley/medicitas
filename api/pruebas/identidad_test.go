package pruebas

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// TestIdentidad: sin token 401, token falsificado 401, rol que no corresponde 403,
// rol correcto 2xx. La firma se valida contra las llaves públicas de Keycloak.
//
// Cubre: RF17, criterio de aceptación 3
func TestIdentidad(t *testing.T) {
	e := NuevoEscenario(t)
	ruta := "/disponibilidad?profesional_id=" + e.Prof.String() + "&servicio_id=" + e.Serv30.String() + "&fecha=" + e.Fecha(0)

	r := Pedir(t, http.MethodGet, ruta, "", nil)
	require.Equal(t, http.StatusUnauthorized, r.Status, "sin token")

	valido := Token(t, usuarioRecepcion)
	partes := strings.Split(valido, ".")
	firma := []byte(partes[2])
	if firma[10] == 'A' {
		firma[10] = 'B'
	} else {
		firma[10] = 'A'
	}
	alterado := partes[0] + "." + partes[1] + "." + string(firma)
	r = Pedir(t, http.MethodGet, ruta, alterado, nil)
	require.Equal(t, http.StatusUnauthorized, r.Status, "firma alterada")

	// Token bien formado pero firmado con una llave que no es de Keycloak.
	llave, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	falso := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "http://localhost:8080/realms/medicitas", "aud": "medicitas-api",
		"sub": subDelToken(t, valido), "exp": time.Now().Add(time.Hour).Unix(),
		"realm_access": map[string]any{"roles": []string{"admin_clinica"}},
	})
	falso.Header["kid"] = "llave-inventada"
	firmado, err := falso.SignedString(llave)
	require.NoError(t, err)
	r = Pedir(t, http.MethodGet, ruta, firmado, nil)
	require.Equal(t, http.StatusUnauthorized, r.Status, "firmado por otro")

	// "none" y basura
	r = Pedir(t, http.MethodGet, ruta, "esto.no.es-un-token", nil)
	require.Equal(t, http.StatusUnauthorized, r.Status)

	// Rol que no corresponde: 403
	r = e.Reservar(t, Token(t, "prof1.central-cartago"), e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 9, 0))
	require.Equal(t, http.StatusForbidden, r.Status, "el profesional no reserva")
	r = Pedir(t, http.MethodGet, ruta, Token(t, "plataforma"), nil)
	require.Equal(t, http.StatusForbidden, r.Status, "plataforma no ve agendas")

	// Rol correcto: 200
	for _, u := range []string{usuarioRecepcion, usuarioAdmin, "prof1.central-cartago"} {
		r = Pedir(t, http.MethodGet, ruta, Token(t, u), nil)
		require.Equal(t, http.StatusOK, r.Status, u)
	}
}
