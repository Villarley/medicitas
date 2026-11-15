# Caché de disponibilidad (Redis)

La consulta de RF7 es la que más se ejecuta, así que la servimos desde Redis. Redis nunca es la fuente de verdad: si falla o no tiene el dato, se calcula desde PostgreSQL.

## Clave y alcance

Una entrada por profesional, fecha y servicio:

```
disp:{clinica_id}:{profesional_id}:{fecha}:{servicio_id}:g{gen}:v{ver}
```

- `clinica_id` sale de la identidad, nunca de la petición, así que una clínica no puede leer entradas de otra (I5).
- `servicio_id` está porque la duración cambia la grilla de espacios.
- Guardamos la lista de espacios ya calculada. Al leerla filtramos los que ya pasaron, porque la entrada puede tener unos minutos.
- `gen` y `ver` son números de versión que viven en Redis (ver abajo).

Alternativas que descartamos: cachear por profesional y fecha sin servicio (habría que recalcular por servicio en cada lectura, y lo caro es la lectura de la base, no el cálculo), y cachear el rango completo de RF8 (cualquier reserva lo invalidaría entero). RF8 se arma llamando a RF7 día por día, así que aprovecha las mismas entradas.

## Política de invalidación: versiones

Dos contadores:

| Contador | Sube cuando |
|---|---|
| `dispver:{clinica}:{profesional}:{fecha}` | Se confirma una reserva, cancelación o reprogramación ese día (en reprogramar, el día viejo y el nuevo) |
| `dispgen:{clinica}:{profesional}` | Cambia el horario, un bloqueo o los servicios del profesional (afecta a todas las fechas) |

La lectura es así (`internal/disponibilidad/servicio.go`):

1. Leer `gen` y `ver` antes de tocar la base.
2. Buscar la clave con esas versiones. Si está, responder con `"fuente": "cache"`.
3. Si no, calcular desde la base y guardar con las versiones del paso 1. Responder con `"fuente": "base"`.

La escritura sube el contador **después del commit** y antes de responder. Así, cuando el cliente recibe el 201 de una reserva, cualquier consulta siguiente ya usa una clave nueva (I8).

Por qué versiones y no borrar la clave: con borrado hay una carrera. Un lector calcula con datos viejos, la escritura confirma y borra, y el lector guarda su cálculo viejo después del borrado. Esa entrada mentiría hasta que expire. Con versiones, ese lector guarda bajo la versión que leyó en el paso 1, que ya nadie vuelve a pedir. La entrada vieja queda huérfana y el TTL la limpia.

El TTL (`CACHE_TTL_SEGUNDOS`, 300 por defecto) es solo limpieza. La coherencia no depende de él.

## Si Redis falla

- Al leer: se calcula desde la base y no se guarda. La API sigue respondiendo bien, más lenta.
- Al invalidar: se registra el error en el log. Es la única ventana de incoherencia que aceptamos: si Redis acepta lecturas pero rechaza el `INCR`, una entrada puede quedar vieja hasta su TTL. La reserva en sí nunca se ve afectada, porque la decide PostgreSQL. Si alguien intenta reservar un espacio que la caché mostró como libre, recibe 409.

## Pruebas

- `TestCacheNoMiente`: consulta (fuente base), consulta otra vez (fuente cache), reserva uno de los espacios devueltos y consulta enseguida. El espacio ya no aparece (I8). Cancela y el espacio vuelve (I7). Reprograma y se actualizan las dos fechas.
- `TestDisponibilidadSinCacheIgualConCache`: la respuesta de Redis coincide con la calculada desde la base.

## Medición antes y después

`docker compose exec api go run ./cmd/medircache` hace 300 consultas sobre 10 combinaciones de profesional y fecha de la semilla. Primero manda el encabezado `X-Sin-Cache: 1`, que la API solo respeta con `CACHE_PERMITIR_BYPASS=1`; después las manda normales.

Resultado en nuestra corrida (contenedores en una sola máquina, base con unas 6000 citas):

| Escenario | Promedio (ms) | p50 (ms) | p95 (ms) | Aciertos de caché |
|---|---|---|---|---|
| Sin caché (solo PostgreSQL) | 2.62 | 2.54 | 3.41 | 0% |
| Con caché (Redis) | 1.26 | 1.17 | 1.69 | 96% |

Con caché el tiempo baja a la mitad. El 4% restante son las primeras consultas de cada combinación. La ganancia real es quitarle carga a la base cuando muchas recepciones consultan a la vez, y eso se mide con las pruebas de carga de la Etapa 2.
