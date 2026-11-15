package pruebas

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCarreraUltimoEspacio(t *testing.T) {
	e := NuevoEscenario(t)
	tokens := []string{Token(t, usuarioRecepcion), Token(t, usuarioRecepcion2)}
	const rondas = 25

	for ronda := 0; ronda < rondas; ronda++ {
		// 8 espacios de 30 min por mañana; cada ronda usa uno distinto.
		inicio := e.Hora(ronda/8, 8, 30*(ronda%8))
		var (
			arranque   = make(chan struct{})
			wg         sync.WaitGroup
			respuestas = make([]Respuesta, 2)
		)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-arranque // los dos salen juntos
				respuestas[i] = e.Reservar(t, tokens[i], e.Pacientes[i], e.Prof, e.Serv30, inicio)
			}(i)
		}
		close(arranque)
		wg.Wait()

		codigos := map[int]int{}
		for _, r := range respuestas {
			codigos[r.Status]++
			if r.Status == http.StatusConflict {
				require.Equal(t, "ESPACIO_TOMADO", r.Codigo(), "ronda %d", ronda)
			}
		}
		require.Equal(t, map[int]int{http.StatusCreated: 1, http.StatusConflict: 1}, codigos,
			"ronda %d: %s | %s", ronda, respuestas[0].Cuerpo, respuestas[1].Cuerpo)
		require.Equal(t, 1, ContarVigentesQueCruzan(t, e.Prof, inicio, inicio.Add(30*time.Minute)), "ronda %d", ronda)
	}
}

func TestCarreraMismoPaciente(t *testing.T) {
	e := NuevoEscenario(t)
	tokens := []string{Token(t, usuarioRecepcion), Token(t, usuarioRecepcion2)}
	for ronda := 0; ronda < 10; ronda++ {
		inicio := e.Hora(ronda/8, 13, 30*(ronda%8))
		arranque := make(chan struct{})
		var wg sync.WaitGroup
		respuestas := make([]Respuesta, 2)
		profs := []any{e.Prof, e.Prof2}
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-arranque
				respuestas[i] = Pedir(t, http.MethodPost, "/citas", tokens[i], map[string]any{
					"paciente_id": e.Pacientes[4], "profesional_id": profs[i], "servicio_id": e.Serv30,
					"inicio": inicio.Format(time.RFC3339)})
			}(i)
		}
		close(arranque)
		wg.Wait()
		ganadoras, ocupado := 0, 0
		for _, r := range respuestas {
			if r.Status == http.StatusCreated {
				ganadoras++
			}
			if r.Status == http.StatusConflict && r.Codigo() == "PACIENTE_OCUPADO" {
				ocupado++
			}
		}
		require.Equal(t, 1, ganadoras, "ronda %d: %s | %s", ronda, respuestas[0].Cuerpo, respuestas[1].Cuerpo)
		require.Equal(t, 1, ocupado, "ronda %d", ronda)
	}
}
