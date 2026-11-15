package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func registrarGestion(r chi.Router, d *Deps, permitir func([]string) func(http.Handler) http.Handler) {
	// Plataforma
	r.With(permitir(soloPlataforma)).Get("/plataforma/clinicas", manejar(d.listarClinicas))
	r.With(permitir(soloPlataforma)).Post("/plataforma/clinicas", manejar(d.crearClinica))

	// Usuarios
	r.With(permitir(soloAdmin)).Get("/usuarios", manejar(d.listarUsuarios))
	r.With(permitir(soloAdmin)).Post("/usuarios", manejar(d.crearUsuario))
	r.With(permitir(soloAdmin)).Patch("/usuarios/{id}", manejar(d.cambiarRolUsuario))
	r.With(permitir(soloAdmin)).Delete("/usuarios/{id}", manejar(d.bajaUsuario))

	// Catálogo: lo leen todos los roles de clínica, lo escribe solo el admin.
	r.With(permitir(rolesDeClinica)).Get("/especialidades", manejar(d.listarEspecialidades))
	r.With(permitir(soloAdmin)).Post("/especialidades", manejar(d.crearEspecialidad))
	r.With(permitir(rolesDeClinica)).Get("/servicios", manejar(d.listarServicios))
	r.With(permitir(soloAdmin)).Post("/servicios", manejar(d.crearServicio))
	r.With(permitir(soloAdmin)).Patch("/servicios/{id}", manejar(d.actualizarServicio))

	r.With(permitir(rolesDeClinica)).Get("/profesionales", manejar(d.listarProfesionales))
	r.With(permitir(rolesDeClinica)).Get("/profesionales/{id}", manejar(d.obtenerProfesional))
	r.With(permitir(soloAdmin)).Post("/profesionales", manejar(d.crearProfesional))
	r.With(permitir(soloAdmin)).Put("/profesionales/{id}/servicios", manejar(d.asignarServicios))

	// Horario y bloqueos: el profesional solo los suyos.
	r.With(permitir(adminYProfesional)).Get("/profesionales/{id}/horarios", manejar(d.listarHorarios))
	r.With(permitir(adminYProfesional)).Post("/profesionales/{id}/horarios", manejar(d.crearHorario))
	r.With(permitir(adminYProfesional)).Delete("/horarios/{id}", manejar(d.borrarHorario))

	r.With(permitir(adminYProfesional)).Get("/profesionales/{id}/bloqueos", manejar(d.listarBloqueos))
	r.With(permitir(adminYProfesional)).Post("/profesionales/{id}/bloqueos", manejar(d.crearBloqueo))
	r.With(permitir(adminYProfesional)).Delete("/bloqueos/{id}", manejar(d.borrarBloqueo))
	r.With(permitir(rolesDeClinica)).Get("/profesionales/{id}/conflictos", manejar(d.listarConflictos))

	// Pacientes: el profesional solo lee.
	r.With(permitir(rolesDeClinica)).Get("/pacientes", manejar(d.listarPacientes))
	r.With(permitir(adminYRecepcion)).Post("/pacientes", manejar(d.crearPaciente))
	r.With(permitir(rolesDeClinica)).Get("/pacientes/{id}", manejar(d.obtenerPaciente))
	r.With(permitir(adminYRecepcion)).Patch("/pacientes/{id}", manejar(d.actualizarPaciente))
	r.With(permitir(rolesDeClinica)).Get("/pacientes/{id}/historial", manejar(d.historialPaciente))

	// Notas clínicas: solo de citas propias (lo exige el motor).
	r.With(permitir(soloProfesional)).Post("/citas/{id}/nota", manejar(d.escribirNota))
	r.With(permitir(soloProfesional)).Get("/citas/{id}/nota", manejar(d.leerNota))

	// Bitácora
	r.With(permitir(soloAdmin)).Get("/auditoria", manejar(d.consultarAuditoria))

	// Operación diaria
	r.With(permitir(rolesDeClinica)).Get("/agenda", manejar(d.agendaDelDia))
	r.With(permitir(adminYRecepcion)).Get("/reportes/citas-por-estado", manejar(d.citasPorEstado))
}
