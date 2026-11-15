package pruebas

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDeadlockIntercambio(t *testing.T) {
	e := NuevoEscenario(t)
	tokens := []string{Token(t, usuarioRecepcion), Token(t, usuarioRecepcion2)}
	ctx, cancelar := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelar()

	for ronda := 0; ronda < 5; ronda++ {
		horaA, horaB := e.Hora(ronda, 9, 0), e.Hora(ronda, 10, 0)
		var a, b citaJSON
		r := e.Reservar(t, tokens[0], e.Pacientes[0], e.Prof, e.Serv30, horaA)
		require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
		r.JSON(t, &a)
		r = e.Reservar(t, tokens[0], e.Pacientes[1], e.Prof, e.Serv30, horaB)
		require.Equal(t, http.StatusCreated, r.Status, string(r.Cuerpo))
		r.JSON(t, &b)

		arranque := make(chan struct{})
		var wg sync.WaitGroup
		respuestas := make([]Respuesta, 2)
		movimientos := []struct {
			id    uuid.UUID
			hacia time.Time
		}{{a.ID, horaB}, {b.ID, horaA}}
		for i := range movimientos {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-arranque
				respuestas[i] = Pedir(t, http.MethodPost, "/citas/"+movimientos[i].id.String()+"/reprogramar",
					tokens[i], map[string]any{"inicio": movimientos[i].hacia.Format(time.RFC3339)})
			}(i)
		}
		close(arranque)
		listo := make(chan struct{})
		go func() { wg.Wait(); close(listo) }()
		select {
		case <-listo:
		case <-ctx.Done():
			t.Fatal("la reprogramación cruzada se colgó")
		}
		for _, r := range respuestas {
			require.Contains(t, []int{http.StatusCreated, http.StatusConflict, http.StatusServiceUnavailable}, r.Status, string(r.Cuerpo))
		}
		require.LessOrEqual(t, ContarVigentesQueCruzan(t, e.Prof, horaA, horaA.Add(30*time.Minute)), 1)
		require.LessOrEqual(t, ContarVigentesQueCruzan(t, e.Prof, horaB, horaB.Add(30*time.Minute)), 1)
	}
}
