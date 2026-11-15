package disponibilidad

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"medicitas/api/internal/cache"
	"medicitas/api/internal/db"
	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
)

const FormatoFecha = "2006-01-02"

type Servicio struct {
	Store *db.Store
	Cache *cache.Cache
	// Ahora se puede reemplazar en pruebas.
	Ahora func() time.Time
}

type Resultado struct {
	Fecha         string    `json:"fecha"`
	ProfesionalID uuid.UUID `json:"profesional_id"`
	ServicioID    uuid.UUID `json:"servicio_id"`
	Espacios      []Espacio `json:"espacios"`
	Fuente        string    `json:"fuente"`
}

func (s *Servicio) Consultar(ctx context.Context, ident identidad.Identidad, prof, servicio uuid.UUID, fecha string, usarCache bool) (*Resultado, error) {
	dia, err := time.Parse(FormatoFecha, fecha)
	if err != nil {
		return nil, errores.Invalido("fecha debe tener formato AAAA-MM-DD")
	}
	clinica := ident.ClinicaID.UUID
	ahora := s.ahora()

	// 1. Versiones ANTES de leer la base (ver el comentario del paquete cache).
	clave, cacheOK := "", false
	if usarCache && s.Cache != nil {
		clave, cacheOK = s.Cache.Clave(ctx, clinica, prof, servicio, fecha)
		if cacheOK {
			if b, ok := s.Cache.Leer(ctx, clave); ok {
				var espacios []Espacio
				if json.Unmarshal(b, &espacios) == nil {
					return &Resultado{Fecha: fecha, ProfesionalID: prof, ServicioID: servicio,
						Espacios: sinPasados(espacios, ahora), Fuente: "cache"}, nil
				}
			}
		}
	}

	// 2. Base de datos.
	var espacios []Espacio
	err = s.Store.EnTransaccion(ctx, ident, func(tx pgx.Tx) error {
		var e error
		espacios, e = CalcularDesdeBase(ctx, tx, prof, servicio, dia, ahora)
		return e
	})
	if err != nil {
		return nil, err
	}

	// 3. Guardar con las versiones leídas en el paso 1.
	if cacheOK {
		if b, err := json.Marshal(espacios); err == nil {
			s.Cache.Guardar(ctx, clave, b)
		}
	}
	return &Resultado{Fecha: fecha, ProfesionalID: prof, ServicioID: servicio, Espacios: espacios, Fuente: "base"}, nil
}

func CalcularDesdeBase(ctx context.Context, tx pgx.Tx, prof, servicio uuid.UUID, dia time.Time, ahora time.Time) ([]Espacio, error) {
	var zonaNombre string
	if err := tx.QueryRow(ctx, `SELECT zona_horaria FROM clinica WHERE id = app_clinica_actual()`).Scan(&zonaNombre); err != nil {
		return nil, err
	}
	zona, err := time.LoadLocation(zonaNombre)
	if err != nil {
		return nil, err
	}

	var profActivo bool
	err = tx.QueryRow(ctx, `SELECT activo FROM profesional WHERE id = $1`, prof).Scan(&profActivo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errores.NoEncontrado("Profesional no encontrado")
	}
	if err != nil {
		return nil, err
	}
	var duracion int
	var srvActivo bool
	err = tx.QueryRow(ctx, `SELECT duracion_min, activo FROM servicio WHERE id = $1`, servicio).Scan(&duracion, &srvActivo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errores.NoEncontrado("Servicio no encontrado")
	}
	if err != nil {
		return nil, err
	}
	var habilitado bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM profesional_servicio WHERE profesional_id = $1 AND servicio_id = $2)`,
		prof, servicio).Scan(&habilitado); err != nil {
		return nil, err
	}
	if !habilitado {
		return nil, errores.Nuevo(422, "SERVICIO_NO_HABILITADO", "El profesional no atiende ese servicio")
	}
	if !profActivo || !srvActivo {
		return []Espacio{}, nil
	}

	anio, mes, d := dia.Date()
	iniDia := time.Date(anio, mes, d, 0, 0, 0, 0, zona)
	finDia := iniDia.AddDate(0, 0, 1)
	diaSemana := int(iniDia.Weekday())
	if diaSemana == 0 {
		diaSemana = 7 // ISO: domingo = 7
	}

	filas, err := tx.Query(ctx,
		`SELECT hora_inicio, hora_fin FROM horario_atencion WHERE profesional_id = $1 AND dia_semana = $2`,
		prof, diaSemana)
	if err != nil {
		return nil, err
	}
	var bloques []Bloque
	for filas.Next() {
		var hi, hf pgtype.Time
		if err := filas.Scan(&hi, &hf); err != nil {
			return nil, err
		}
		bloques = append(bloques, Bloque{InicioMin: int(hi.Microseconds / 60e6), FinMin: int(hf.Microseconds / 60e6)})
	}
	if err := filas.Err(); err != nil {
		return nil, err
	}

	bloqueos, err := intervalos(ctx, tx,
		`SELECT inicio, fin FROM bloqueo_agenda
		  WHERE profesional_id = $1 AND tstzrange(inicio, fin, '[)') && tstzrange($2, $3, '[)')`,
		prof, iniDia, finDia)
	if err != nil {
		return nil, err
	}
	// Solape con el día escrito como rango sobre inicio, para que el índice parcial
	// idx_cita_vigente_prof_inicio (profesional_id, inicio) lo resuelva en el Index
	// Cond en vez de leer todas las citas vigentes del profesional y filtrar. Es
	// exacto: un servicio dura como mucho 480 min, así que una cita que se cruza
	// con el día empezó a lo sumo 8 h antes. Ver docs/indices.md.
	citas, err := intervalos(ctx, tx,
		`SELECT inicio, fin FROM cita
		  WHERE profesional_id = $1 AND estado = 'reservada'
		    AND inicio >= $2::timestamptz - interval '480 minutes' AND inicio < $3 AND fin > $2`,
		prof, iniDia, finDia)
	if err != nil {
		return nil, err
	}
	return CalcularEspacios(bloques, bloqueos, citas, duracion, iniDia, zona, ahora), nil
}

func intervalos(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]Intervalo, error) {
	filas, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(filas, func(f pgx.CollectableRow) (Intervalo, error) {
		var i Intervalo
		err := f.Scan(&i.Inicio, &i.Fin)
		return i, err
	})
}

func sinPasados(es []Espacio, ahora time.Time) []Espacio {
	out := make([]Espacio, 0, len(es))
	for _, e := range es {
		if !e.Inicio.Before(ahora) {
			out = append(out, e)
		}
	}
	return out
}

func (s *Servicio) ahora() time.Time {
	if s.Ahora != nil {
		return s.Ahora()
	}
	return time.Now()
}

func FechaLocal(t time.Time, zona *time.Location) string {
	return t.In(zona).Format(FormatoFecha)
}
