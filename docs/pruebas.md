# Informe de pruebas

Con el stack arriba (`docker compose up --build`), un solo comando corre las unitarias y las de integración:

```
docker compose exec api go test -count=1 ./...
```

- No hace falta preparar datos. Cada prueba de integración crea su propio escenario (profesionales, servicios, pacientes y, cuando hace falta, usuarios de Keycloak) en una fecha lejana elegida al azar. Por eso se pueden correr muchas veces seguidas sin limpiar la base.
- Las de integración hablan con la pila real: la API por HTTP, PostgreSQL, Keycloak (tokens de verdad, por password grant) y Redis.
- `-count=1` evita que Go devuelva resultados guardados de una corrida anterior.

Una prueba sola, con detalle: `docker compose exec api go test -count=1 -run Carrera -v ./pruebas/`.

### Resultado de la última corrida

```
ok   medicitas/api/internal/db              0.004s
ok   medicitas/api/internal/disponibilidad  0.002s
ok   medicitas/api/internal/http            0.004s
ok   medicitas/api/pruebas                  6.981s
```

34 pruebas (5 unitarias, 29 de integración), todas en verde. La suite se corrió dos veces seguidas sobre la misma base para confirmar que es repetible.

## Unitarias (lógica aislada, sin base ni red)

| Prueba | Archivo | Qué aísla |
|---|---|---|
| `TestCalcularEspacios` | `internal/disponibilidad/calculo_test.go` | El cálculo puro de espacios de RF7: bloques de horario, bloqueos, citas, duración, zona horaria, espacios pasados |
| `TestTraducir` | `internal/db/errores_test.go` | Traducción de errores del motor (SQLSTATE, restricciones, MC001 a MC011) a HTTP |
| `TestInstante` | `internal/http/ayudas_test.go` | Fechas de filtros: AAAA-MM-DD en la zona de la clínica, "hasta" que incluye el día, RFC 3339 |
| `TestPaginacion` | `internal/http/ayudas_test.go` | Límites y desplazamiento válidos e inválidos |
| `TestRequerido` | `internal/http/ayudas_test.go` | Campos obligatorios y su mensaje |

## Integración (`api/pruebas/`)

| Prueba | Archivo | RF | Invariantes | Otros |
|---|---|---|---|---|
| `TestAltaDeClinica` | plataforma_test.go | RF1, RF17 | I5 | matriz |
| `TestGestionDeUsuarios` | plataforma_test.go | RF2, RF17 | | matriz |
| `TestKeycloakAdmin` | keycloak_test.go | RF17, base de RF1/RF2 | | |
| `TestIdentidad` | identidad_test.go | RF17 | | criterio 3 |
| `TestCatalogo` | catalogo_test.go | RF3, RF4 | I4 | matriz |
| `TestDesactivarServicioInvalidaCache` | catalogo_test.go | RF3, RF7 | I8 | |
| `TestHorarios` | agenda_test.go | RF5, RF7 | I3, I8 | |
| `TestBloqueoYConflictos` | agenda_test.go | RF6, RF7, RF10 | I3, I8 | |
| `TestProfesionalSoloLoPropio` | agenda_test.go | RF5, RF6 | | matriz |
| `TestAgendaDelDia` | agenda_test.go | RF20 | | |
| `TestHorarioYBloqueos` | reglas_test.go | RF9 | I3 | |
| `TestDuracionInmutable` | reglas_test.go | RF9 | I4 | |
| `TestReprogramarSolapandoConsigoMisma` | reglas_test.go | RF10 | I7 | |
| `TestCicloDeLaCita` | ciclo_test.go | RF9 a RF13 | | criterio 2 |
| `TestCarreraUltimoEspacio` | carrera_test.go | RF9 | I1 | criterio 6 |
| `TestCarreraMismoPaciente` | carrera_test.go | RF9 | I2 | |
| `TestDeadlockIntercambio` | deadlock_test.go | RF10 | I1, I7 | |
| `TestCacheNoMiente` | cache_test.go | RF7, RF9, RF11 | I7, I8 | criterio 5 |
| `TestDisponibilidadSinCacheIgualConCache` | cache_test.go | RF7 | I8 | |
| `TestDisponibilidadPorServicio` | cache_test.go | RF8 | | |
| `TestPacientes` | pacientes_test.go | RF14 | | matriz |
| `TestHistorialDelPaciente` | pacientes_test.go | RF16, RF10, RF11 | | |
| `TestNotaClinica` | pacientes_test.go | RF15, RF12, RF18 | | matriz |
| `TestBitacoraDeContacto` | pacientes_test.go | RF18, RF19 | I6 | |
| `TestBitacoraInmutable` | bitacora_test.go | RF18 | I6 | |
| `TestCitasPorEstado` | pacientes_test.go | RF21 | | |
| `TestFugaEnCitasYDisponibilidad` | aislamiento_test.go | RF7, RF9 a RF11 | I5 | criterio 4 |
| `TestFugaEnRutasDeGestion` | aislamiento_gestion_test.go | RF2 a RF6, RF14 a RF16, RF19 a RF21 | I5 | criterio 4 |
| `TestErroresNoTumbanLaAPI` | errores_test.go | | | requisitos de operación 2 y 3 |

## Trazabilidad por requisito funcional

| RF | Pruebas |
|---|---|
| RF1 alta de clínica | `TestAltaDeClinica` |
| RF2 usuarios | `TestGestionDeUsuarios`, `TestKeycloakAdmin`, `TestFugaEnRutasDeGestion` |
| RF3 catálogo de servicios | `TestCatalogo`, `TestDesactivarServicioInvalidaCache` |
| RF4 profesionales y servicios | `TestCatalogo` |
| RF5 horario semanal | `TestHorarios`, `TestProfesionalSoloLoPropio` |
| RF6 bloqueos y conflictos | `TestBloqueoYConflictos`, `TestProfesionalSoloLoPropio` |
| RF7 disponibilidad | `TestCacheNoMiente`, `TestDisponibilidadSinCacheIgualConCache`, `TestHorarios`, `TestBloqueoYConflictos` |
| RF8 disponibilidad por servicio | `TestDisponibilidadPorServicio` |
| RF9 reservar | `TestCarreraUltimoEspacio`, `TestCarreraMismoPaciente`, `TestCicloDeLaCita`, `TestHorarioYBloqueos`, `TestDuracionInmutable` |
| RF10 reprogramar | `TestCicloDeLaCita`, `TestReprogramarSolapandoConsigoMisma`, `TestDeadlockIntercambio`, `TestBloqueoYConflictos` |
| RF11 cancelar | `TestCicloDeLaCita`, `TestCacheNoMiente` |
| RF12 desenlace | `TestCicloDeLaCita`, `TestNotaClinica` |
| RF13 estados | `TestCicloDeLaCita` |
| RF14 pacientes | `TestPacientes` |
| RF15 nota clínica | `TestNotaClinica` |
| RF16 historial | `TestHistorialDelPaciente` |
| RF17 identidad | `TestIdentidad`, `TestKeycloakAdmin`, `TestAltaDeClinica`, `TestGestionDeUsuarios` |
| RF18 bitácora | `TestBitacoraDeContacto`, `TestNotaClinica`, `TestBitacoraInmutable` |
| RF19 consulta de bitácora | `TestBitacoraDeContacto` |
| RF20 agenda del día | `TestAgendaDelDia` |
| RF21 conteo por estado | `TestCitasPorEstado` |

## Trazabilidad por invariante

| Invariante | Cómo se garantiza | Pruebas |
|---|---|---|
| I1 sin solapes por profesional | Restricción de exclusión `cita_sin_solape_profesional` | `TestCarreraUltimoEspacio` (25 rondas concurrentes), `TestDeadlockIntercambio` |
| I2 sin solapes por paciente | Restricción `cita_sin_solape_paciente` | `TestCarreraMismoPaciente` |
| I3 dentro de horario, fuera de bloqueos | Trigger de reserva + candado consultivo; no se borra un bloque de horario con citas dentro | `TestHorarioYBloqueos`, `TestHorarios`, `TestBloqueoYConflictos` |
| I4 duración la define el servicio | Trigger calcula `fin`; UPDATE de campos de la cita prohibido; la API no cambia `duracion_min` | `TestDuracionInmutable`, `TestCatalogo` |
| I5 aislamiento entre clínicas | Clínica desde la identidad + RLS `FORCE` + FK compuestas | `TestFugaEnCitasYDisponibilidad`, `TestFugaEnRutasDeGestion`, `TestAltaDeClinica` |
| I6 bitácora inmutable | Sin permisos UPDATE/DELETE + triggers | `TestBitacoraInmutable`, `TestBitacoraDeContacto`. La capa de triggers se verificó a mano (ver `docs/seguridad.md`) |
| I7 cancelar/reprogramar libera el espacio | Restricción parcial `WHERE estado = 'reservada'` | `TestCacheNoMiente`, `TestReprogramarSolapandoConsigoMisma`, `TestDeadlockIntercambio` |
| I8 la caché no contradice la base | Invalidación por versiones después del commit | `TestCacheNoMiente`, `TestDisponibilidadSinCacheIgualConCache`, `TestDesactivarServicioInvalidaCache`, `TestHorarios`, `TestBloqueoYConflictos` |

## Criterios de aceptación

| Criterio | Cómo se comprueba |
|---|---|
| 1. Levanta solo | `docker compose up --build` desde el repo recién clonado (README) |
| 2. Ciclo de la cita | `TestCicloDeLaCita` y `scripts/demo.sh` |
| 3. Identidad | `TestIdentidad` |
| 4. Aislamiento | `TestFugaEnCitasYDisponibilidad`, `TestFugaEnRutasDeGestion` |
| 5. La caché no miente | `TestCacheNoMiente` |
| 6. Carrera con una ganadora | `TestCarreraUltimoEspacio` |
| 7. Las pruebas corren | el comando de arriba |

## Las pruebas discriminan?:

Para la bitácora lo comprobamos con una mutación: quitamos el registro de lectura de `GET /pacientes/{id}`, levantamos esa versión de la API y corrimos `TestBitacoraDeContacto`. Falló como debía:

```
--- FAIL: TestBitacoraDeContacto
    Error: "[{… escritura contacto_paciente …}]" should have 2 item(s), but has 1
    Messages: alta + una lectura; el listado no cuenta
```

La prueba de carrera discrimina por construcción: en cada una de sus 25 rondas exige exactamente un 201, un 409 y una sola cita vigente en la base. Si faltara la restricción de exclusión, las dos reservas confirmarían y la ronda fallaría.
