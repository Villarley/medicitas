package http

import (
	"net/http"

	"medicitas/api/internal/citas"
)

// POST /citas  (RF9): 201 concedida, 409 ESPACIO_TOMADO / PACIENTE_OCUPADO, 422 o 404 solicitud inválida.
func (d *Deps) reservar(w http.ResponseWriter, r *http.Request) {
	var s citas.SolicitudReserva
	if err := LeerJSON(r, &s); err != nil {
		Error(w, err)
		return
	}
	c, err := d.Citas.Reservar(r.Context(), Ident(r), s)
	if err != nil {
		Error(w, err)
		return
	}
	JSON(w, http.StatusCreated, c)
}

// GET /citas/{id}
func (d *Deps) obtenerCita(w http.ResponseWriter, r *http.Request) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		Error(w, err)
		return
	}
	c, err := d.Citas.Obtener(r.Context(), Ident(r), id)
	if err != nil {
		Error(w, err)
		return
	}
	JSON(w, http.StatusOK, c)
}

// POST /citas/{id}/reprogramar  (RF10): 201 con la cita nueva.
func (d *Deps) reprogramar(w http.ResponseWriter, r *http.Request) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		Error(w, err)
		return
	}
	var s citas.SolicitudReprogramar
	if err := LeerJSON(r, &s); err != nil {
		Error(w, err)
		return
	}
	c, err := d.Citas.Reprogramar(r.Context(), Ident(r), id, s)
	if err != nil {
		Error(w, err)
		return
	}
	JSON(w, http.StatusCreated, c)
}

// POST /citas/{id}/cancelar  (RF11)
func (d *Deps) cancelar(w http.ResponseWriter, r *http.Request) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		Error(w, err)
		return
	}
	var s struct {
		Motivo string `json:"motivo"`
	}
	if err := LeerJSON(r, &s); err != nil {
		Error(w, err)
		return
	}
	c, err := d.Citas.Cancelar(r.Context(), Ident(r), id, s.Motivo)
	if err != nil {
		Error(w, err)
		return
	}
	JSON(w, http.StatusOK, c)
}

// POST /citas/{id}/desenlace  (RF12)
func (d *Deps) desenlace(w http.ResponseWriter, r *http.Request) {
	id, err := ParamUUID(r, "id")
	if err != nil {
		Error(w, err)
		return
	}
	var s struct {
		Desenlace string `json:"desenlace"`
	}
	if err := LeerJSON(r, &s); err != nil {
		Error(w, err)
		return
	}
	c, err := d.Citas.RegistrarDesenlace(r.Context(), Ident(r), id, s.Desenlace)
	if err != nil {
		Error(w, err)
		return
	}
	JSON(w, http.StatusOK, c)
}
