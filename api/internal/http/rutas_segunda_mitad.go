package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registrarSegundaMitad es donde va la segunda mitad de las rutas (guía de Fabián).
// Cada ruta sigue el mismo patrón que citas.go y disponibilidad.go:
//
//  1. r.With(permitir(<roles de la matriz>)).<Método>("<ruta>", d.<manejador>)
//  2. El manejador lee la entrada con LeerJSON / ParamUUID / QueryUUID.
//  3. Todo acceso a la base va dentro de d.Store.EnTransaccion(ctx, Ident(r), func(tx pgx.Tx) error { ... }),
//     con parámetros $1, $2... (nunca texto armado a mano).
//  4. Responde con JSON(w, código, valor) o Error(w, err).
//
// Rutas que faltan (sección 6 del contrato):
//
//	RF1  POST/GET /plataforma/clinicas                       soloPlataforma
//	RF2  GET/POST /usuarios, PATCH/DELETE /usuarios/{id}      soloAdmin
//	RF3  GET/POST /especialidades, GET/POST /servicios, PATCH /servicios/{id}   soloAdmin (GET también rolesDeClinica)
//	RF4  GET/POST /profesionales, PUT /profesionales/{id}/servicios            soloAdmin (GET también rolesDeClinica)
//	RF5  GET/POST /profesionales/{id}/horarios, DELETE /horarios/{id}         adminYProfesional
//	RF6  GET/POST /profesionales/{id}/bloqueos, DELETE /bloqueos/{id},
//	     GET /profesionales/{id}/conflictos                                   adminYProfesional
//	RF14 GET/POST /pacientes, GET/PATCH /pacientes/{id}       rolesDeClinica (POST y PATCH: adminYRecepcion)
//	RF15 GET/POST /citas/{id}/nota                            soloProfesional
//	RF16 GET /pacientes/{id}/historial                        rolesDeClinica
//	RF19 GET /auditoria                                       soloAdmin
//	RF20 GET /agenda                                          rolesDeClinica
//	RF21 GET /reportes/citas-por-estado                       adminYRecepcion
func registrarSegundaMitad(r chi.Router, d *Deps, permitir func([]string) func(http.Handler) http.Handler) {
	_, _, _ = r, d, permitir
}
