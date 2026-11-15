// Package identidad define quién hace la petición, ya validado.
package identidad

import (
	"context"

	"github.com/google/uuid"
)

const (
	AdminPlataforma = "admin_plataforma"
	AdminClinica    = "admin_clinica"
	Recepcion       = "recepcion"
	Profesional     = "profesional"
)

type Identidad struct {
	Sub       string
	UsuarioID uuid.UUID
	ClinicaID uuid.NullUUID // vacío solo para admin_plataforma
	Rol       string
}

type claveCtx struct{}

func EnContexto(ctx context.Context, i Identidad) context.Context {
	return context.WithValue(ctx, claveCtx{}, i)
}

func DeContexto(ctx context.Context) (Identidad, bool) {
	i, ok := ctx.Value(claveCtx{}).(Identidad)
	return i, ok
}
