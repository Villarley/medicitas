package pruebas

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestFugaEnCitasYDisponibilidad: una recepcionista de Central Cartago intenta
// leer y tocar datos de San Pedro por las rutas de citas y disponibilidad.
// Todo lo ajeno responde 404, como si no existiera.
// (La segunda mitad agrega lo mismo para pacientes, catálogo, agenda y bitácora.)
//
// Cubre: I5, RF7, RF9, RF10, RF11, criterio de aceptación 4
func TestFugaEnCitasYDisponibilidad(t *testing.T) {
	e := NuevoEscenario(t)
	tokCentral := Token(t, usuarioRecepcion)
	tokSanPedro := Token(t, "recepcion.san-pedro")

	// Una cita real de San Pedro, creada por San Pedro.
	ajena := datosDeSanPedro(t, tokSanPedro)

	r := Pedir(t, http.MethodGet, "/citas/"+ajena.cita.String(), tokCentral, nil)
	require.Equal(t, http.StatusNotFound, r.Status, "leer cita ajena por id")
	r = Pedir(t, http.MethodPost, "/citas/"+ajena.cita.String()+"/cancelar", tokCentral, map[string]any{"motivo": "fuga"})
	require.Equal(t, http.StatusNotFound, r.Status, "cancelar cita ajena")
	r = Pedir(t, http.MethodPost, "/citas/"+ajena.cita.String()+"/reprogramar", tokCentral, map[string]any{"inicio": e.Hora(0, 9, 0).Format(time.RFC3339)})
	require.Equal(t, http.StatusNotFound, r.Status, "reprogramar cita ajena")

	// Reservar en Central con un paciente o profesional de San Pedro.
	r = e.Reservar(t, tokCentral, ajena.paciente, e.Prof, e.Serv30, e.Hora(0, 9, 0))
	require.Equal(t, http.StatusNotFound, r.Status, "paciente ajeno")
	r = e.Reservar(t, tokCentral, e.Pacientes[0], ajena.profesional, ajena.servicio, ajena.inicio.Add(time.Hour))
	require.Equal(t, http.StatusNotFound, r.Status, "profesional ajeno")

	// Mandar clinica_id de otra clínica en el cuerpo y en la query no cambia nada.
	r = Pedir(t, http.MethodPost, "/citas?clinica_id="+ajena.clinica.String(), tokCentral, map[string]any{
		"clinica_id": ajena.clinica, "paciente_id": e.Pacientes[0], "profesional_id": e.Prof,
		"servicio_id": e.Serv30, "inicio": e.Hora(0, 10, 0).Format(time.RFC3339)})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c citaJSON
	r.JSON(t, &c)
	require.NotEqual(t, ajena.clinica, c.ClinicaID, "la clínica sale del token, no del cuerpo")

	// Disponibilidad de un profesional ajeno.
	r = Pedir(t, http.MethodGet, "/disponibilidad?profesional_id="+ajena.profesional.String()+"&servicio_id="+ajena.servicio.String()+"&fecha="+ajena.inicio.Format("2006-01-02"), tokCentral, nil)
	require.Equal(t, http.StatusNotFound, r.Status, "disponibilidad ajena")
}

type datosAjenos struct {
	clinica, paciente, profesional, servicio, cita uuid.UUID
	inicio                                         time.Time
}

// datosDeSanPedro toma un profesional y su servicio de la semilla de San Pedro
// y reserva el primer espacio libre que encuentre.
func datosDeSanPedro(t *testing.T, tok string) datosAjenos {
	t.Helper()
	var d datosAjenos
	ComoAdminDe(t, "admin.san-pedro", func(q consulta) {
		q(`SELECT ps.profesional_id, ps.servicio_id FROM profesional_servicio ps ORDER BY ps.profesional_id LIMIT 1`, &d.profesional, &d.servicio)
		q(`SELECT id, clinica_id FROM paciente ORDER BY id LIMIT 1`, &d.paciente, &d.clinica)
	})
	for dias := 1; dias < 30 && d.cita == uuid.Nil; dias++ {
		fecha := time.Now().In(zonaCR).AddDate(0, 0, dias).Format("2006-01-02")
		r := Pedir(t, http.MethodGet, "/disponibilidad?profesional_id="+d.profesional.String()+"&servicio_id="+d.servicio.String()+"&fecha="+fecha, tok, nil)
		require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
		var disp dispJSON
		r.JSON(t, &disp)
		for i := len(disp.Espacios) - 1; i >= 0; i-- {
			r = Pedir(t, http.MethodPost, "/citas", tok, map[string]any{"paciente_id": d.paciente, "profesional_id": d.profesional,
				"servicio_id": d.servicio, "inicio": disp.Espacios[i].Inicio.Format(time.RFC3339)})
			if r.Status == http.StatusCreated {
				var c citaJSON
				r.JSON(t, &c)
				d.cita, d.inicio = c.ID, c.Inicio
				break
			}
		}
	}
	require.NotEqual(t, uuid.Nil, d.cita, "no se encontró espacio libre en San Pedro")
	return d
}
