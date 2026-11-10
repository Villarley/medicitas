// Package cache guarda en Redis la disponibilidad calculada (RF7).
//
// Coherencia (I8) por versiones, no por borrado de claves:
//
//	dispgen:{clinica}:{profesional}          sube con cambios de horario, bloqueos o servicios
//	dispver:{clinica}:{profesional}:{fecha}  sube con cada reserva, cancelación o reprogramación
//	disp:{clinica}:{profesional}:{fecha}:{servicio}:g{gen}:v{ver}  el valor
//
// El lector lee gen y ver ANTES de ir a la base y guarda con esas versiones.
// Si una escritura confirma en medio, sube la versión y lo que el lector guarde
// queda bajo una clave vieja que nadie vuelve a pedir. El TTL solo limpia.
// Redis nunca es la fuente de verdad: si falla, se calcula desde la base.
package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *redis.Client
	ttl time.Duration
}

func Nueva(url string, ttl time.Duration) (*Cache, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opt.DialTimeout = 500 * time.Millisecond
	opt.ReadTimeout = 300 * time.Millisecond
	opt.WriteTimeout = 300 * time.Millisecond
	return &Cache{rdb: redis.NewClient(opt), ttl: ttl}, nil
}

func claveGen(c, p uuid.UUID) string { return fmt.Sprintf("dispgen:%s:%s", c, p) }
func claveVer(c, p uuid.UUID, fecha string) string {
	return fmt.Sprintf("dispver:%s:%s:%s", c, p, fecha)
}

// Clave arma la clave del valor con las versiones vigentes. ok=false si Redis no respondió.
func (k *Cache) Clave(ctx context.Context, clinica, prof, servicio uuid.UUID, fecha string) (string, bool) {
	vals, err := k.rdb.MGet(ctx, claveGen(clinica, prof), claveVer(clinica, prof, fecha)).Result()
	if err != nil {
		slog.Warn("redis no responde; se usa la base", "error", err)
		return "", false
	}
	gen, ver := entero(vals[0]), entero(vals[1])
	return fmt.Sprintf("disp:%s:%s:%s:%s:g%d:v%d", clinica, prof, fecha, servicio, gen, ver), true
}

func (k *Cache) Leer(ctx context.Context, clave string) ([]byte, bool) {
	b, err := k.rdb.Get(ctx, clave).Bytes()
	if err != nil {
		if err != redis.Nil {
			slog.Warn("redis get falló", "error", err)
		}
		return nil, false
	}
	return b, true
}

func (k *Cache) Guardar(ctx context.Context, clave string, valor []byte) {
	if err := k.rdb.Set(ctx, clave, valor, k.ttl).Err(); err != nil {
		slog.Warn("redis set falló", "error", err)
	}
}

// InvalidarFecha se llama DESPUÉS del commit de una reserva, cancelación o reprogramación.
func (k *Cache) InvalidarFecha(ctx context.Context, clinica, prof uuid.UUID, fecha string) {
	clave := claveVer(clinica, prof, fecha)
	pipe := k.rdb.TxPipeline()
	pipe.Incr(ctx, clave)
	pipe.Expire(ctx, clave, 30*24*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("no se pudo invalidar la caché", "clave", clave, "error", err)
	}
}

// InvalidarProfesional se llama después de cambiar horarios, bloqueos o servicios del profesional.
func (k *Cache) InvalidarProfesional(ctx context.Context, clinica, prof uuid.UUID) {
	if err := k.rdb.Incr(ctx, claveGen(clinica, prof)).Err(); err != nil {
		slog.Error("no se pudo invalidar la caché", "profesional", prof, "error", err)
	}
}

func (k *Cache) Ping(ctx context.Context) error { return k.rdb.Ping(ctx).Err() }

func entero(v any) int64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
