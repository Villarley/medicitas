package pruebas

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dispJSON struct {
	Fuente   string `json:"fuente"`
	Espacios []struct {
		Inicio time.Time `json:"inicio"`
		Fin    time.Time `json:"fin"`
	} `json:"espacios"`
}

func consultar(t *testing.T, tok string, e *Escenario, semanas int) dispJSON {
	t.Helper()
	r := Pedir(t, http.MethodGet, "/disponibilidad?profesional_id="+e.Prof.String()+"&servicio_id="+e.Serv30.String()+"&fecha="+e.Fecha(semanas), tok, nil)
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	var d dispJSON
	r.JSON(t, &d)
	return d
}

func contiene(d dispJSON, inicio time.Time) bool {
	for _, x := range d.Espacios {
		if x.Inicio.Equal(inicio) {
			return true
		}
	}
	return false
}

func TestCacheNoMiente(t *testing.T) {
	e := NuevoEscenario(t)
	tok := Token(t, usuarioRecepcion)

	d := consultar(t, tok, e, 0)
	require.Equal(t, "base", d.Fuente)
	require.Len(t, d.Espacios, 16, "jornada partida 8-12 y 13-17 con servicio de 30 min")
	d = consultar(t, tok, e, 0)
	require.Equal(t, "cache", d.Fuente, "la segunda consulta tiene que salir de Redis")

	elegido := d.Espacios[3].Inicio
	r := e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv30, elegido)
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	var c citaJSON
	r.JSON(t, &c)

	d = consultar(t, tok, e, 0)
	require.False(t, contiene(d, elegido), "I8: la caché no puede ofrecer el espacio recién reservado")
	require.Len(t, d.Espacios, 15)

	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/cancelar", tok, map[string]any{"motivo": "Prueba de caché"})
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	d = consultar(t, tok, e, 0)
	require.True(t, contiene(d, elegido), "I7: cancelar libera el espacio de inmediato")

	// Reprogramar libera el viejo y ocupa el nuevo en la misma consulta siguiente.
	r = e.Reservar(t, tok, e.Pacientes[1], e.Prof, e.Serv30, elegido)
	require.Equal(t, http.StatusCreated, r.Status)
	r.JSON(t, &c)
	destino := e.Hora(1, 15, 0)
	_ = consultar(t, tok, e, 1) // deja la otra fecha en caché
	r = Pedir(t, http.MethodPost, "/citas/"+c.ID.String()+"/reprogramar", tok, map[string]any{"inicio": destino.Format(time.RFC3339)})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
	require.True(t, contiene(consultar(t, tok, e, 0), elegido))
	require.False(t, contiene(consultar(t, tok, e, 1), destino))
}

func TestDisponibilidadSinCacheIgualConCache(t *testing.T) {
	e := NuevoEscenario(t)
	tok := Token(t, usuarioRecepcion)
	r := e.Reservar(t, tok, e.Pacientes[0], e.Prof, e.Serv30, e.Hora(0, 14, 0))
	require.Equal(t, http.StatusCreated, r.Status)
	conCache := consultar(t, tok, e, 0)
	conCache = consultar(t, tok, e, 0)
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/api/v1/disponibilidad?profesional_id="+e.Prof.String()+"&servicio_id="+e.Serv30.String()+"&fecha="+e.Fecha(0), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Sin-Cache", "1")
	resp, err := clienteHTTP.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var sinCache dispJSON
	require.NoError(t, jsonDecode(resp, &sinCache))
	require.Equal(t, "base", sinCache.Fuente)
	require.Equal(t, len(sinCache.Espacios), len(conCache.Espacios))
}

func TestDisponibilidadPorServicio(t *testing.T) {
	e := NuevoEscenario(t)
	tok := Token(t, usuarioRecepcion)
	r := Pedir(t, http.MethodGet, "/disponibilidad/servicio?servicio_id="+e.Serv45.String()+"&desde="+e.Fecha(0)+"&hasta="+e.Lunes.AddDate(0, 0, 6).Format("2006-01-02"), tok, nil)
	require.Equal(t, http.StatusOK, r.Status, string(r.Cuerpo))
	var res struct {
		Profesionales []struct {
			Dias []struct {
				Espacios []any `json:"espacios"`
			} `json:"dias"`
		} `json:"profesionales"`
	}
	r.JSON(t, &res)
	require.Len(t, res.Profesionales, 2)
	require.Len(t, res.Profesionales[0].Dias, 1, "solo el lunes tiene horario")
	require.Len(t, res.Profesionales[0].Dias[0].Espacios, 10, "45 min: 5 por bloque de 4 horas")

	r = Pedir(t, http.MethodGet, "/disponibilidad/servicio?servicio_id="+e.Serv45.String()+"&desde="+e.Fecha(0)+"&hasta="+e.Fecha(3), tok, nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, "más de 14 días")
}
