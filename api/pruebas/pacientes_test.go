package pruebas

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPacientes(t *testing.T) {
	tokRecep := Token(t, usuarioRecepcion)
	tokProf := Token(t, usuarioProf1)
	ident := "T-" + uuid.NewString()[:13]
	cuerpo := map[string]any{"identificacion": ident, "nombre": "Lucía", "apellidos": "Prueba Mora",
		"fecha_nacimiento": "1992-07-01", "telefono": "8888-0000"}

	r := Pedir(t, http.MethodPost, "/pacientes", tokProf, cuerpo)
	require.Equal(t, http.StatusForbidden, r.Status, "el profesional no registra pacientes")

	r = Pedir(t, http.MethodPost, "/pacientes", tokRecep, cuerpo)
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var p struct {
		ID              uuid.UUID `json:"id"`
		FechaNacimiento string    `json:"fecha_nacimiento"`
		Telefono        *string   `json:"telefono"`
		Correo          *string   `json:"correo"`
	}
	r.JSON(t, &p)
	require.Equal(t, "1992-07-01", p.FechaNacimiento)

	r = Pedir(t, http.MethodPost, "/pacientes", tokRecep, cuerpo)
	require.Equal(t, http.StatusConflict, r.Status, "identificación repetida en la clínica")
	require.Equal(t, "DUPLICADO", r.Codigo())

	for nombre, malo := range map[string]map[string]any{
		"sin nombre":      {"identificacion": "X-" + ident, "apellidos": "A", "fecha_nacimiento": "1990-01-01"},
		"fecha mal":       {"identificacion": "X-" + ident, "nombre": "A", "apellidos": "B", "fecha_nacimiento": "01/02/1990"},
		"fecha futura":    {"identificacion": "X-" + ident, "nombre": "A", "apellidos": "B", "fecha_nacimiento": "2999-01-01"},
		"correo inválido": {"identificacion": "X-" + ident, "nombre": "A", "apellidos": "B", "fecha_nacimiento": "1990-01-01", "correo": "no-es-correo"},
		"tipo equivocado": {"identificacion": 123, "nombre": "A", "apellidos": "B", "fecha_nacimiento": "1990-01-01"},
	} {
		r = Pedir(t, http.MethodPost, "/pacientes", tokRecep, malo)
		require.Equal(t, http.StatusUnprocessableEntity, r.Status, nombre)
	}

	// Búsqueda: por identificación exacta o por parte del nombre; sin contacto.
	r = Pedir(t, http.MethodGet, "/pacientes?buscar="+ident, tokProf, nil)
	require.Equal(t, []uuid.UUID{p.ID}, IDs(t, r))
	require.NotContains(t, string(r.Cuerpo), "telefono", "los listados no traen contacto")
	require.Contains(t, IDs(t, Pedir(t, http.MethodGet, "/pacientes?buscar=prueba%20mora&limite=200", tokRecep, nil)), p.ID)
	r = Pedir(t, http.MethodGet, "/pacientes?limite=0", tokRecep, nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)

	// PATCH: cambia lo que viene, "" borra un dato de contacto.
	r = Pedir(t, http.MethodPatch, "/pacientes/"+p.ID.String(), tokRecep, map[string]any{"correo": "lucia@medicitas.test", "telefono": ""})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	r.JSON(t, &p)
	require.Nil(t, p.Telefono)
	require.Equal(t, "lucia@medicitas.test", *p.Correo)
	r = Pedir(t, http.MethodPatch, "/pacientes/"+p.ID.String(), tokProf, map[string]any{"nombre": "X"})
	require.Equal(t, http.StatusForbidden, r.Status, "el profesional solo lee")
	r = Pedir(t, http.MethodPatch, "/pacientes/"+p.ID.String(), tokRecep, map[string]any{"nombre": " "})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)

	r = Pedir(t, http.MethodGet, "/pacientes/"+p.ID.String(), tokProf, nil)
	require.Equal(t, http.StatusOK, r.Status)
	r = Pedir(t, http.MethodGet, "/pacientes/"+uuid.NewString(), tokRecep, nil)
	require.Equal(t, http.StatusNotFound, r.Status)
}

func TestHistorialDelPaciente(t *testing.T) {
	e := NuevoEscenario(t)
	tokRecep := Token(t, usuarioRecepcion)
	pac := e.Pacientes[0]

	r := e.Reservar(t, tokRecep, pac, e.Prof, e.Serv30, e.Hora(0, 9, 0))
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c1 citaJSON
	r.JSON(t, &c1)
	r = Pedir(t, http.MethodPost, "/citas/"+c1.ID.String()+"/reprogramar", tokRecep, map[string]any{"inicio": e.Hora(1, 9, 0).Format(time.RFC3339)})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c2 citaJSON
	r.JSON(t, &c2)
	r = e.Reservar(t, tokRecep, pac, e.Prof, e.Serv30, e.Hora(2, 9, 0))
	var c3 citaJSON
	r.JSON(t, &c3)
	r = Pedir(t, http.MethodPost, "/citas/"+c3.ID.String()+"/cancelar", tokRecep, map[string]any{"motivo": "Viaje"})
	require.Equal(t, http.StatusOK, r.Status)

	for _, u := range []string{usuarioRecepcion, usuarioAdmin, usuarioProf1} {
		r = Pedir(t, http.MethodGet, "/pacientes/"+pac.String()+"/historial", Token(t, u), nil)
		require.Equal(t, http.StatusOK, r.Status, u)
		var h []struct {
			ID           uuid.UUID     `json:"id"`
			Estado       string        `json:"estado"`
			CitaOrigenID uuid.NullUUID `json:"cita_origen_id"`
			Servicio     string        `json:"servicio"`
		}
		r.JSON(t, &h)
		require.Len(t, h, 3, u)
		require.Equal(t, []string{"cancelada", "reservada", "reprogramada"}, []string{h[0].Estado, h[1].Estado, h[2].Estado})
		require.Equal(t, c1.ID, h[1].CitaOrigenID.UUID, "la nueva apunta a la reprogramada")
		require.NotEmpty(t, h[0].Servicio)
	}
}

func TestBitacoraDeContacto(t *testing.T) {
	tokRecep := Token(t, usuarioRecepcion)
	tokProf := Token(t, usuarioProf1)
	pac := NuevoPaciente(t, tokRecep, true)
	sinContacto := NuevoPaciente(t, tokRecep, false)

	Pedir(t, http.MethodGet, "/pacientes?buscar=Ana", tokProf, nil)
	r := Pedir(t, http.MethodGet, "/pacientes/"+pac.String(), tokProf, nil)
	require.Equal(t, http.StatusOK, r.Status)
	Pedir(t, http.MethodGet, "/pacientes/"+sinContacto.String(), tokProf, nil)

	b := Bitacora(t, "paciente_id="+pac.String())
	require.Len(t, b, 2, "alta + una lectura; el listado no cuenta")
	require.Equal(t, "lectura", b[0].Accion, "la más reciente primero")
	require.Equal(t, MiUsuario(t, usuarioProf1), b[0].UsuarioID)
	require.Equal(t, "escritura", b[1].Accion)
	require.Equal(t, MiUsuario(t, usuarioRecepcion), b[1].UsuarioID)
	for _, x := range b {
		require.Equal(t, "contacto_paciente", x.Recurso)
		require.Equal(t, pac, x.PacienteID)
	}
	require.Empty(t, Bitacora(t, "paciente_id="+sinContacto.String()), "sin contacto no hay nada que auditar")

	// Filtros por usuario y por fechas.
	soloProf := Bitacora(t, "paciente_id="+pac.String()+"&usuario_id="+MiUsuario(t, usuarioProf1).String())
	require.Len(t, soloProf, 1)
	hoy := time.Now().In(zonaCR).Format("2006-01-02")
	ayer := time.Now().In(zonaCR).AddDate(0, 0, -1).Format("2006-01-02")
	require.Len(t, Bitacora(t, "paciente_id="+pac.String()+"&desde="+hoy+"&hasta="+hoy), 2)
	require.Empty(t, Bitacora(t, "paciente_id="+pac.String()+"&hasta="+ayer))

	// Solo el admin consulta.
	for _, u := range []string{usuarioRecepcion, usuarioProf1, "plataforma"} {
		r = Pedir(t, http.MethodGet, "/auditoria", Token(t, u), nil)
		require.Equal(t, http.StatusForbidden, r.Status, u)
	}
	r = Pedir(t, http.MethodGet, "/auditoria?desde=ayer", Token(t, usuarioAdmin), nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
}

func TestNotaClinica(t *testing.T) {
	p := NuevoProfConUsuario(t)
	tokDueno := Token(t, p.Username)
	tokRecep := Token(t, usuarioRecepcion)
	pac := NuevoPaciente(t, tokRecep, false)
	cita := p.CitaPasada(t, pac)
	ruta := "/citas/" + cita.String() + "/nota"
	nota := map[string]any{"contenido": "Paciente estable. Control en un mes."}

	// Todavía reservada: el motor la rechaza.
	r := Pedir(t, http.MethodPost, ruta, tokDueno, nota)
	require.Equal(t, http.StatusForbidden, r.Status)
	require.Equal(t, "NOTA_NO_PERMITIDA", r.Codigo())

	r = Pedir(t, http.MethodPost, "/citas/"+cita.String()+"/desenlace", tokDueno, map[string]any{"desenlace": "atendida"})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))

	// Otros roles: la ruta ni los deja pasar. Otro profesional: el motor lo rechaza.
	for _, u := range []string{usuarioRecepcion, usuarioAdmin} {
		r = Pedir(t, http.MethodPost, ruta, Token(t, u), nota)
		require.Equal(t, http.StatusForbidden, r.Status, u)
	}
	r = Pedir(t, http.MethodPost, ruta, Token(t, usuarioProf1), nota)
	require.Equal(t, http.StatusForbidden, r.Status)
	require.Equal(t, "NOTA_NO_PERMITIDA", r.Codigo())

	r = Pedir(t, http.MethodPost, ruta, tokDueno, map[string]any{"contenido": "   "})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
	r = Pedir(t, http.MethodPost, ruta, tokDueno, nota)
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var n struct {
		ID        uuid.UUID `json:"id"`
		Contenido string    `json:"contenido"`
	}
	r.JSON(t, &n)
	r = Pedir(t, http.MethodPost, ruta, tokDueno, nota)
	require.Equal(t, http.StatusConflict, r.Status, "una nota por cita")

	r = Pedir(t, http.MethodGet, ruta, tokDueno, nil)
	require.Equal(t, http.StatusOK, r.Status)
	require.True(t, strings.HasPrefix(string(r.Cuerpo), "{"))
	r = Pedir(t, http.MethodGet, ruta, Token(t, usuarioProf1), nil)
	require.Equal(t, http.StatusNotFound, r.Status, "la nota de otro profesional no existe para él")
	r = Pedir(t, http.MethodGet, ruta, tokRecep, nil)
	require.Equal(t, http.StatusForbidden, r.Status)

	b := Bitacora(t, "paciente_id="+pac.String())
	require.Len(t, b, 2)
	require.Equal(t, []string{"lectura", "escritura"}, []string{b[0].Accion, b[1].Accion})
	for _, x := range b {
		require.Equal(t, "nota_clinica", x.Recurso)
		require.Equal(t, n.ID, x.RecursoID.UUID)
		require.Equal(t, p.UsuarioID, x.UsuarioID)
	}
}

func TestCitasPorEstado(t *testing.T) {
	e := NuevoEscenario(t)
	tokRecep := Token(t, usuarioRecepcion)
	ruta := "/reportes/citas-por-estado?desde=" + e.Fecha(0) + "&hasta=" + e.Fecha(0)
	contar := func() map[string]int {
		r := Pedir(t, http.MethodGet, ruta, tokRecep, nil)
		require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
		var m map[string]int
		r.JSON(t, &m)
		require.Len(t, m, 5, "los cinco estados, aunque estén en cero")
		return m
	}
	antes := contar()
	var ultima citaJSON
	for i, h := range []int{8, 9, 10} {
		r := e.Reservar(t, tokRecep, e.Pacientes[i], e.Prof, e.Serv30, e.Hora(0, h, 0))
		require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
		r.JSON(t, &ultima)
	}
	r := Pedir(t, http.MethodPost, "/citas/"+ultima.ID.String()+"/cancelar", tokRecep, map[string]any{"motivo": "x"})
	require.Equal(t, http.StatusOK, r.Status)
	despues := contar()
	require.Equal(t, antes["reservada"]+2, despues["reservada"])
	require.Equal(t, antes["cancelada"]+1, despues["cancelada"])

	r = Pedir(t, http.MethodGet, ruta, Token(t, usuarioProf1), nil)
	require.Equal(t, http.StatusForbidden, r.Status)
	r = Pedir(t, http.MethodGet, "/reportes/citas-por-estado?desde="+e.Fecha(1)+"&hasta="+e.Fecha(0), tokRecep, nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status)
}
