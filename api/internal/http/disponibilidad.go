package http

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"medicitas/api/internal/disponibilidad"
	"medicitas/api/internal/errores"
)

func (d *Deps) consultarDisponibilidad(w http.ResponseWriter, r *http.Request) {
	prof, err := QueryUUID(r, "profesional_id")
	if err != nil {
		Error(w, err)
		return
	}
	srv, err := QueryUUID(r, "servicio_id")
	if err != nil {
		Error(w, err)
		return
	}
	fecha := r.URL.Query().Get("fecha")
	// X-Sin-Cache solo se respeta si CACHE_PERMITIR_BYPASS=1 (medición antes/después).
	usarCache := !(d.CachePermitirBypass && r.Header.Get("X-Sin-Cache") == "1")
	res, err := d.Disponibilidad.Consultar(r.Context(), Ident(r), prof, srv, fecha, usarCache)
	if err != nil {
		Error(w, err)
		return
	}
	JSON(w, http.StatusOK, res)
}

type diaConEspacios struct {
	Fecha    string                   `json:"fecha"`
	Espacios []disponibilidad.Espacio `json:"espacios"`
}

type profesionalDisponible struct {
	ProfesionalID uuid.UUID        `json:"profesional_id"`
	Nombre        string           `json:"nombre"`
	Dias          []diaConEspacios `json:"dias"`
}

func (d *Deps) disponibilidadPorServicio(w http.ResponseWriter, r *http.Request) {
	srv, err := QueryUUID(r, "servicio_id")
	if err != nil {
		Error(w, err)
		return
	}
	desde, err1 := time.Parse(disponibilidad.FormatoFecha, r.URL.Query().Get("desde"))
	hasta, err2 := time.Parse(disponibilidad.FormatoFecha, r.URL.Query().Get("hasta"))
	if err1 != nil || err2 != nil || hasta.Before(desde) {
		Error(w, errores.Invalido("desde y hasta deben ser fechas AAAA-MM-DD con desde <= hasta"))
		return
	}
	if hasta.Sub(desde) > 13*24*time.Hour {
		Error(w, errores.Invalido("El rango máximo es de 14 días"))
		return
	}
	ident := Ident(r)
	type prof struct {
		id     uuid.UUID
		nombre string
	}
	var profs []prof
	err = d.Store.EnTransaccion(r.Context(), ident, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM servicio WHERE id = $1)`, srv).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return errores.NoEncontrado("Servicio no encontrado")
		}
		filas, err := tx.Query(r.Context(),
			`SELECT p.id, p.nombre FROM profesional p
			   JOIN profesional_servicio ps ON ps.profesional_id = p.id
			  WHERE ps.servicio_id = $1 AND p.activo ORDER BY p.nombre`, srv)
		if err != nil {
			return err
		}
		profs, err = pgx.CollectRows(filas, func(f pgx.CollectableRow) (prof, error) {
			var p prof
			return p, f.Scan(&p.id, &p.nombre)
		})
		return err
	})
	if err != nil {
		Error(w, err)
		return
	}
	resultado := []profesionalDisponible{}
	for _, p := range profs {
		item := profesionalDisponible{ProfesionalID: p.id, Nombre: p.nombre}
		for dia := desde; !dia.After(hasta); dia = dia.AddDate(0, 0, 1) {
			res, err := d.Disponibilidad.Consultar(r.Context(), ident, p.id, srv, dia.Format(disponibilidad.FormatoFecha), true)
			if err != nil {
				Error(w, err)
				return
			}
			if len(res.Espacios) > 0 {
				item.Dias = append(item.Dias, diaConEspacios{Fecha: res.Fecha, Espacios: res.Espacios})
			}
		}
		if len(item.Dias) > 0 {
			resultado = append(resultado, item)
		}
	}
	JSON(w, http.StatusOK, map[string]any{"servicio_id": srv, "profesionales": resultado})
}
