package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"medicitas/api/internal/auth"
	"medicitas/api/internal/cache"
	"medicitas/api/internal/citas"
	"medicitas/api/internal/db"
	"medicitas/api/internal/disponibilidad"
	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
	"medicitas/api/internal/keycloak"
)

// Deps son las piezas que comparten todos los manejadores.
type Deps struct {
	Store               *db.Store
	Cache               *cache.Cache
	Validador           *auth.Validador
	Keycloak            *keycloak.Admin
	Disponibilidad      *disponibilidad.Servicio
	Citas               *citas.Servicio
	CachePermitirBypass bool
}

// Atajos de roles para la matriz de permisos (sección 5 del contrato).
var (
	soloPlataforma    = []string{identidad.AdminPlataforma}
	soloAdmin         = []string{identidad.AdminClinica}
	adminYRecepcion   = []string{identidad.AdminClinica, identidad.Recepcion}
	rolesDeClinica    = []string{identidad.AdminClinica, identidad.Recepcion, identidad.Profesional}
	soloProfesional   = []string{identidad.Profesional}
	adminYProfesional = []string{identidad.AdminClinica, identidad.Profesional}
)

func Router(d *Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, recuperar)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { Error(w, errores.NoEncontrado("Ruta no encontrada")) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		Error(w, errores.Nuevo(http.StatusMethodNotAllowed, "METODO_NO_PERMITIDO", "Método no permitido en esta ruta"))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/salud", func(w http.ResponseWriter, _ *http.Request) {
			JSON(w, http.StatusOK, map[string]string{"estado": "ok"})
		})

		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(d.Validador, d.Store, Error))
			permitir := func(roles []string) func(http.Handler) http.Handler { return auth.RequiereRoles(Error, roles...) }

			// RF7, RF8
			r.With(permitir(rolesDeClinica)).Get("/disponibilidad", d.consultarDisponibilidad)
			r.With(permitir(rolesDeClinica)).Get("/disponibilidad/servicio", d.disponibilidadPorServicio)

			// RF9 a RF13
			r.With(permitir(adminYRecepcion)).Post("/citas", d.reservar)
			r.With(permitir(rolesDeClinica)).Get("/citas/{id}", d.obtenerCita)
			r.With(permitir(adminYRecepcion)).Post("/citas/{id}/reprogramar", d.reprogramar)
			r.With(permitir(adminYRecepcion)).Post("/citas/{id}/cancelar", d.cancelar)
			r.With(permitir(rolesDeClinica)).Post("/citas/{id}/desenlace", d.desenlace)

			// Segunda mitad del proyecto (ver rutas_segunda_mitad.go).
			registrarSegundaMitad(r, d, permitir)
		})
	})
	return r
}
