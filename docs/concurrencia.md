# Transacciones y concurrencia

## La pregunta del proyecto

Dos recepcionistas reservan el último espacio de la agenda exactamente al mismo tiempo. Lo que pasa en MediCitas:

1. Las dos peticiones abren su propia transacción y hacen el `INSERT` de la cita.
2. La tabla `cita` tiene la restricción `cita_sin_solape_profesional`: no pueden existir dos citas en estado `reservada` del mismo profesional cuyos rangos `[inicio, fin)` se crucen.
3. La segunda transacción encuentra la fila de la primera, que todavía no confirmó, y espera.
4. Cuando la primera confirma, la segunda falla con el código 23P01 (violación de exclusión).
5. La API traduce ese error a `409 ESPACIO_TOMADO`. La primera recibe `201`.

Siempre hay exactamente una ganadora, sin importar el orden en que lleguen ni cuántas réplicas de la API haya, porque quien decide es el motor y no el código.

Lo demostramos con `TestCarreraUltimoEspacio` (`api/pruebas/carrera_test.go`): 25 rondas, cada una con dos goroutines que salen al mismo tiempo con tokens de dos recepcionistas distintas. En cada ronda afirmamos un 201, un 409 y una sola cita vigente en la base.

```
docker compose exec api go test -count=1 -run Carrera -v ./pruebas/
```

`TestCarreraMismoPaciente` hace lo mismo para I2: el mismo paciente con dos profesionales a la misma hora. Gana una y la otra recibe `409 PACIENTE_OCUPADO` (restricción `cita_sin_solape_paciente`).

## Nivel de aislamiento: READ COMMITTED

Lo fijamos de forma explícita en cada transacción (`pgx.TxOptions{IsoLevel: pgx.ReadCommitted}` en `internal/db/db.go`), aunque es el nivel por defecto de PostgreSQL, para que nadie dependa de una configuración que no se ve.

Por qué este y no otro:

| Alternativa | Qué pasaría | Por qué no |
|---|---|---|
| Revisar con un SELECT antes de insertar, en READ COMMITTED | Las dos transacciones ven el espacio libre y las dos insertan | Es exactamente el error que pide evitar el enunciado |
| REPEATABLE READ | Cada transacción ve una foto fija; el SELECT previo tampoco ve a la otra | No evita el write skew; la restricción sigue siendo necesaria |
| SERIALIZABLE | Sería correcto, pero aborta transacciones por conflictos de predicado que no son reales (dos reservas en espacios distintos que leyeron rangos vecinos) | Más reintentos y más carga sin ganar nada sobre la restricción |
| READ COMMITTED + restricción de exclusión (elegida) | El índice gist de la restricción serializa solo a quienes de verdad chocan | Lo que perdemos: dependemos de que el estado `reservada` sea el único que ocupa espacio, y eso queda escrito en la condición `WHERE` de la restricción |

## Reglas que no son solapes: candado consultivo

I3 dice que una cita vigente cae dentro del horario y fuera de los bloqueos. El trigger `cita_insertar` lo revisa, pero un bloqueo creado al mismo tiempo que una reserva podría no verla. Para eso cada reserva toma un candado consultivo compartido por profesional (`pg_advisory_xact_lock_shared`) y cada cambio de horario o bloqueo toma el exclusivo. Las reservas entre sí no se esperan; un bloqueo sí espera a que terminen las reservas en curso de ese profesional, y viceversa.

## Reprogramar sin estados intermedios (RF10)

Todo pasa en una sola transacción: `SELECT ... FOR UPDATE` de la cita vieja, `UPDATE` a `reprogramada` y el `INSERT` de la nueva con `cita_origen_id`. El UPDATE va primero, así la cita nueva puede cruzarse con su propio espacio viejo (mover una cita de 45 minutos media hora más tarde). Afuera de la transacción nadie ve un momento con la cita duplicada ni con los dos espacios ocupados.

## Deadlock

El caso real: dos recepcionistas intercambian dos citas a la vez, A al espacio de B y B al de A. Cada transacción libera su espacio y queda esperando el de la otra. PostgreSQL detecta el ciclo en un segundo, aborta una con `40P01` y deja seguir a la otra.

`EnTransaccion` (`internal/db/db.go`) reintenta toda la transacción ante `40P01` o `40001`, hasta 3 intentos, con espera de 50 ms, 100 ms y 200 ms más un jitter aleatorio para que no vuelvan a chocar sincronizadas. Si se agotan, la API responde `503 REINTENTAR`. Cada reintento queda en el log de la API.

`TestDeadlockIntercambio` lo provoca cinco veces seguidas y afirma que nada se cuelga, que ninguna respuesta es 500 y que nunca quedan dos citas vigentes en un mismo espacio. En nuestras corridas el log muestra los reintentos (`transacción abortada por el motor; se reintenta`).

## Qué decidió una herramienta por nosotros

No usamos ORM. pgx no cambia el nivel de aislamiento por su cuenta; aun así lo declaramos en cada transacción. El pool de pgx reutiliza conexiones: por eso el perfil (`SET LOCAL ROLE`) y la clínica (`set_config(..., true)`) son locales a la transacción y desaparecen al confirmar o deshacer.
