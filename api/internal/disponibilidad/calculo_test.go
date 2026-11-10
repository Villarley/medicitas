package disponibilidad

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCalcularEspacios(t *testing.T) {
	cr, _ := time.LoadLocation("America/Costa_Rica")
	pa, _ := time.LoadLocation("America/Panama")
	dia := time.Date(2030, 3, 4, 0, 0, 0, 0, cr) // lunes
	h := func(hh, mm int) time.Time { return time.Date(2030, 3, 4, hh, mm, 0, 0, cr) }
	partida := []Bloque{{8 * 60, 12 * 60}, {13 * 60, 17 * 60}}
	antes := time.Date(2030, 1, 1, 0, 0, 0, 0, cr)

	casos := []struct {
		nombre    string
		bloques   []Bloque
		bloqueos  []Intervalo
		citas     []Intervalo
		duracion  int
		ahora     time.Time
		zona      *time.Location
		esperados int
		primero   time.Time
	}{
		{"jornada partida de 30 min", partida, nil, nil, 30, antes, cr, 16, h(8, 0)},
		{"servicio de 45 no cabe al final del bloque", partida, nil, nil, 45, antes, cr, 10, h(8, 0)},
		{"servicio más largo que el bloque", []Bloque{{8 * 60, 8*60 + 20}}, nil, nil, 30, antes, cr, 0, time.Time{}},
		{"bloqueo tapa la mañana", partida, []Intervalo{{h(8, 0), h(12, 0)}}, nil, 30, antes, cr, 8, h(13, 0)},
		{"cita ocupa un espacio", partida, nil, []Intervalo{{h(8, 0), h(8, 30)}}, 30, antes, cr, 15, h(8, 30)},
		{"cita de otro servicio pisa dos espacios", partida, nil, []Intervalo{{h(8, 20), h(8, 40)}}, 30, antes, cr, 14, h(9, 0)},
		{"cita que termina justo al inicio no estorba", partida, nil, []Intervalo{{h(7, 30), h(8, 0)}}, 30, antes, cr, 16, h(8, 0)},
		{"sin horario ese día", nil, nil, nil, 30, antes, cr, 0, time.Time{}},
		{"espacios pasados se descartan", partida, nil, nil, 30, h(10, 5), cr, 11, h(10, 30)},
		{"duración inválida", partida, nil, nil, 0, antes, cr, 0, time.Time{}},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			es := CalcularEspacios(c.bloques, c.bloqueos, c.citas, c.duracion, dia, c.zona, c.ahora)
			require.Len(t, es, c.esperados)
			if c.esperados > 0 {
				require.True(t, es[0].Inicio.Equal(c.primero), "primero %s", es[0].Inicio)
			}
		})
	}

	t.Run("zona de Panamá", func(t *testing.T) {
		diaPa := time.Date(2030, 3, 4, 0, 0, 0, 0, pa)
		es := CalcularEspacios(partida, nil, nil, 60, diaPa, pa, antes)
		require.Len(t, es, 8)
		require.Equal(t, "2030-03-04T08:00:00-05:00", es[0].Inicio.Format(time.RFC3339))
	})
}
