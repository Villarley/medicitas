// Package auth valida el token OIDC de Keycloak y arma la Identidad.
// No hay inicio de sesión propio: solo se verifica lo que firmó el proveedor.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"medicitas/api/internal/db"
	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
)

var rolesDominio = []string{identidad.AdminPlataforma, identidad.AdminClinica, identidad.Recepcion, identidad.Profesional}

type Validador struct {
	kf     keyfunc.Keyfunc
	parser *jwt.Parser
}

func NuevoValidador(ctx context.Context, jwksURL, emisor, audiencia string) (*Validador, error) {
	var kf keyfunc.Keyfunc
	var err error
	for intento := 0; intento < 30; intento++ {
		kf, err = keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
		if err == nil {
			break
		}
		slog.Warn("JWKS no disponible todavía", "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el JWKS: %w", err)
	}
	return &Validador{
		kf: kf,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{"RS256"}),
			jwt.WithIssuer(emisor),
			jwt.WithAudience(audiencia),
			jwt.WithExpirationRequired(),
			jwt.WithLeeway(5*time.Second),
		),
	}, nil
}

func (v *Validador) Validar(token string) (string, []string, error) {
	claims := jwt.MapClaims{}
	if _, err := v.parser.ParseWithClaims(token, claims, v.kf.Keyfunc); err != nil {
		return "", nil, err
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", nil, errors.New("token sin sub")
	}
	var roles []string
	if ra, ok := claims["realm_access"].(map[string]any); ok {
		if lista, ok := ra["roles"].([]any); ok {
			for _, r := range lista {
				if s, ok := r.(string); ok && slices.Contains(rolesDominio, s) {
					roles = append(roles, s)
				}
			}
		}
	}
	return sub, roles, nil
}

func Middleware(v *Validador, store *db.Store, responderError func(http.ResponseWriter, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cabecera := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(cabecera, "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				responderError(w, errores.NoAutenticado("Falta el token de acceso"))
				return
			}
			sub, roles, err := v.Validar(strings.TrimSpace(token))
			if err != nil {
				responderError(w, errores.NoAutenticado("Token inválido o expirado"))
				return
			}
			subUUID, err := uuid.Parse(sub)
			if err != nil {
				responderError(w, errores.NoAutenticado("Identidad inválida"))
				return
			}
			fila, err := store.ResolverIdentidad(r.Context(), subUUID)
			if err != nil {
				responderError(w, err)
				return
			}
			if fila == nil || !fila.Activo || !fila.ClinicaActiva {
				responderError(w, errores.Prohibido("Usuario sin acceso a MediCitas"))
				return
			}
			if !slices.Contains(roles, fila.Rol) {
				responderError(w, errores.Prohibido("El rol del token no coincide con el del usuario"))
				return
			}
			ident := identidad.Identidad{Sub: sub, UsuarioID: fila.UsuarioID, ClinicaID: fila.ClinicaID, Rol: fila.Rol}
			next.ServeHTTP(w, r.WithContext(identidad.EnContexto(r.Context(), ident)))
		})
	}
}

func RequiereRoles(responderError func(http.ResponseWriter, error), roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ident, ok := identidad.DeContexto(r.Context())
			if !ok || !slices.Contains(roles, ident.Rol) {
				responderError(w, errores.Prohibido("Tu rol no permite esta operación"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
