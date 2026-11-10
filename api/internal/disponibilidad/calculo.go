// Package disponibilidad calcula los espacios libres de un profesional (RF7, RF8).
package disponibilidad

import (
	"sort"
	"time"
)

// Bloque es un tramo del horario semanal, en minutos desde la medianoche local.
type Bloque struct {
	InicioMin int
	FinMin    int
}

// Intervalo es un rango [Inicio, Fin) en el tiempo real (bloqueos y citas vigentes).
type Intervalo struct {
	Inicio time.Time
	Fin    time.Time
}

type Espacio struct {
	Inicio time.Time `json:"inicio"`
	Fin    time.Time `json:"fin"`
}

func seCruzan(aIni, aFin, bIni, bFin time.Time) bool {
	return aIni.Before(bFin) && bIni.Before(aFin)
}

// CalcularEspacios no toca la base: recibe el horario del día, los bloqueos y
// las citas vigentes y devuelve los espacios libres para un servicio.
//
// Regla: dentro de cada bloque se proponen inicios cada duracionMin minutos
// desde el inicio del bloque, mientras el servicio quepa completo. Se descartan
// los que se cruzan con un bloqueo o una cita vigente, y los que empiezan
// antes de "ahora". Las horas se arman en la zona de la clínica.
func CalcularEspacios(bloques []Bloque, bloqueos, citas []Intervalo, duracionMin int, fecha time.Time, zona *time.Location, ahora time.Time) []Espacio {
	espacios := []Espacio{}
	if duracionMin <= 0 {
		return espacios
	}
	ordenados := append([]Bloque(nil), bloques...)
	sort.Slice(ordenados, func(i, j int) bool { return ordenados[i].InicioMin < ordenados[j].InicioMin })
	anio, mes, dia := fecha.Date()
	enHora := func(min int) time.Time { return time.Date(anio, mes, dia, min/60, min%60, 0, 0, zona) }
	duracion := time.Duration(duracionMin) * time.Minute

	for _, b := range ordenados {
		finBloque := enHora(b.FinMin)
		for ini := enHora(b.InicioMin); !ini.Add(duracion).After(finBloque); ini = ini.Add(duracion) {
			fin := ini.Add(duracion)
			if ini.Before(ahora) {
				continue
			}
			ocupado := false
			for _, x := range bloqueos {
				if seCruzan(ini, fin, x.Inicio, x.Fin) {
					ocupado = true
					break
				}
			}
			for _, x := range citas {
				if ocupado {
					break
				}
				if seCruzan(ini, fin, x.Inicio, x.Fin) {
					ocupado = true
				}
			}
			if !ocupado {
				espacios = append(espacios, Espacio{Inicio: ini, Fin: fin})
			}
		}
	}
	return espacios
}
