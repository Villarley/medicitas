# MediCitas Etapa 1 · Contrato técnico común

Este archivo es la fuente de verdad que comparten Santiago (API) y Fabián (base de datos, infraestructura y documentación). Si algo cambia, se cambia aquí primero y se avisa al otro.

## 1. Pila elegida

| Pieza | Elección | Por qué (versión corta para la defensa) |
|---|---|---|
| Motor relacional | PostgreSQL 16 | Restricciones de exclusión con rangos (`EXCLUDE USING gist`) para I1 e I2, Row-Level Security nativo para I5, roles con privilegios por columna. |
| API | Go 1.25 + chi (router) + pgx v5 (SQL a mano, sin ORM) | Sin ORM el nivel de aislamiento y cada consulta quedan a la vista; todo parametrizado con `$1, $2...` de pgx. Binario único, concurrencia nativa para la prueba de carrera. |
| Identidad | Keycloak 26 (`keycloak/keycloak:26.0`) | Pedido por el enunciado. Realm importado al arrancar. |
| Caché | Redis 7 | Pedido por el enunciado. Solo caché, nunca fuente de verdad. |
| Pruebas | `go test` (paquete testing + testify) contra la pila real | Un solo comando dentro del contenedor de la API: `docker compose exec api go test -count=1 ./...` |

## 2. Estructura del repositorio y quién hace qué

Primera mitad (Santiago, ya en `main`): infraestructura, base de datos, Keycloak, semilla, el núcleo de la API (conexión, identidad, errores, disponibilidad con caché y ciclo de la cita), sus pruebas y los documentos de concurrencia, identidad y caché.

Segunda mitad (Fabián, guía aparte): las rutas que faltan (RF1 a RF6, RF14 a RF16, RF18 a RF21), sus pruebas, y los documentos de diseño, multi-tenancy, índices, seguridad, informe de pruebas, README completo y colección de peticiones.

```
medicitas/
├── docker-compose.yml, .env.example
├── README.md
├── db/init/
│   ├── 00_roles.sh           roles de base de datos
│   ├── 01_esquema.sql        tablas, 3FN, restricciones de exclusión (I1, I2)
│   ├── 02_reglas.sql         triggers: I3, I4, RF12, RF13, RF15, I6, resolver_identidad
│   ├── 03_seguridad.sql      GRANTs por perfil + RLS
│   ├── 04_indices.sql        índices
│   └── 05_semilla.sql        GENERADO por scripts/generar_semilla.py
├── keycloak/
│   ├── realm-base.json       roles y clients
│   └── medicitas-realm.json  GENERADO, con los usuarios de prueba
├── scripts/
│   ├── generar_semilla.py
│   └── demo.sh               recorrido de una cita (guion del video)
├── api/                      módulo Go (medicitas/api)
│   ├── Dockerfile
│   ├── cmd/api/              main
│   ├── cmd/medircache/       medición de la caché
│   ├── internal/config, errores, identidad
│   ├── internal/db/          pool, EnTransaccion, reintentos, traducción de errores
│   ├── internal/auth/        validación JWT contra JWKS, middleware, RequiereRoles
│   ├── internal/cache/       Redis con invalidación por versiones
│   ├── internal/disponibilidad/  CalcularEspacios (puro) + consulta RF7/RF8
│   ├── internal/citas/       RF9 a RF13
│   ├── internal/keycloak/    Admin REST API (para RF1 y RF2)
│   ├── internal/http/        router y manejadores; rutas_segunda_mitad.go
│   └── pruebas/              pruebas de integración contra la pila real
├── peticiones/               (segunda mitad) colección .http o Bruno
└── docs/
    ├── CONTRATO.md           este archivo
    ├── concurrencia.md, identidad.md, cache.md      (primera mitad)
    ├── diseno.md, multitenancy.md, indices.md,
    │   seguridad.md, pruebas.md                     (segunda mitad)
    └── declaracion-ia.md                            (los dos)
```

## 3. Base de datos (ya escrita y probada)

Los archivos de `db/init/` están en el kit y se probaron levantando la pila real. Lo que la API tiene que saber:

### 3.1 Conexión y contexto por petición

La API se conecta siempre como `medicitas_app` (NOINHERIT, sin permisos propios). Cada petición autenticada hace, en este orden:

1. Fuera de transacción de negocio: `SELECT * FROM resolver_identidad($1)` con el `sub` del token. Devuelve `usuario_id, clinica_id, rol_codigo, activo, clinica_activa`. Si no hay fila o `activo` es falso o `clinica_activa` es falso, la respuesta es 403.
2. Abre la transacción: `BEGIN ISOLATION LEVEL READ COMMITTED`.
3. `SET LOCAL ROLE <perfil>` según el rol: `admin_plataforma -> db_plataforma`, `admin_clinica -> db_admin_clinica`, `recepcion -> db_recepcion`, `profesional -> db_profesional`. El nombre del perfil sale de un diccionario fijo, nunca del texto del token, porque `SET ROLE` no admite parámetros.
4. `SELECT set_config('app.clinica_id', $1, true), set_config('app.usuario_id', $2, true)` con los valores del paso 1 (para plataforma, clinica_id va vacío).
5. Lógica de negocio. 6. `COMMIT`. 7. Recién después del commit, invalidar la caché.

La clínica nunca se lee de la petición. Si un cliente manda `clinica_id` en el cuerpo o en la URL, se ignora; RLS además lo rechazaría.

### 3.2 Tablas

`rol, clinica, usuario, especialidad, servicio, profesional, profesional_servicio, horario_atencion, bloqueo_agenda, paciente, cita, nota_clinica, auditoria`. Columnas exactas en `db/init/01_esquema.sql`. Puntos que importan para el código:

- `cita.fin` lo calcula el trigger desde la duración del servicio. La API manda `fin = inicio` en el INSERT y lee el valor real con `RETURNING`.
- `cita.estado` es el enum `estado_cita`: `reservada, atendida, ausente, cancelada, reprogramada`.
- Reprogramar = en la misma transacción `UPDATE cita SET estado='reprogramada' WHERE id=$1` y luego `INSERT` de la cita nueva con `cita_origen_id` apuntando a la vieja. El UPDATE va primero para que el espacio viejo quede libre si el nuevo se cruza con él.
- `auditoria`: INSERT sin `RETURNING` (recepción y profesional no tienen SELECT sobre esa tabla). El motor sobrescribe `usuario_id` y `ocurrido_en`.
- Horarios en hora local de la clínica (`clinica.zona_horaria`), `dia_semana` ISO (1 lunes ... 7 domingo). Las citas se guardan en `timestamptz`.

### 3.3 Errores del motor y su traducción a HTTP

| SQLSTATE / restricción | Significado | HTTP | `codigo` en la respuesta |
|---|---|---|---|
| 23P01 `cita_sin_solape_profesional` | espacio ya tomado (I1) | 409 | `ESPACIO_TOMADO` |
| 23P01 `cita_sin_solape_paciente` | paciente ya tiene cita a esa hora (I2) | 409 | `PACIENTE_OCUPADO` |
| 23P01 `horario_sin_solape` | bloques de horario solapados | 409 | `HORARIO_SOLAPADO` |
| 23505 | duplicado (código de clínica, identificación de paciente, etc.) | 409 | `DUPLICADO` |
| 23503 | referencia inexistente (en esta clínica) | 422 | `REFERENCIA_INVALIDA` |
| 23514, 22P02, 22007, 22008 | dato inválido | 422 | `SOLICITUD_INVALIDA` |
| 42501 o violación de política RLS | sin permiso en el motor | 403 | `PROHIBIDO` |
| MC001 / MC002 | fuera de horario / dentro de bloqueo (I3) | 422 | `FUERA_DE_HORARIO` / `EN_BLOQUEO` |
| MC003 | transición de estado no permitida | 409 | `TRANSICION_INVALIDA` |
| MC004 | desenlace antes de la hora | 422 | `DESENLACE_PREMATURO` |
| MC005 | nota no permitida | 403 | `NOTA_NO_PERMITIDA` |
| MC006 | bitácora inmutable | 403 | `BITACORA_INMUTABLE` |
| MC007 | campo inmutable de la cita | 422 | `CAMPO_INMUTABLE` |
| MC008 | profesional no atiende ese servicio | 422 | `SERVICIO_NO_HABILITADO` |
| MC009 | profesional o servicio inactivo | 422 | `INACTIVO` |
| MC010 | el profesional solo registra desenlace | 403 | `PROHIBIDO` |
| MC011 | zona horaria inválida | 422 | `SOLICITUD_INVALIDA` |
| 40P01, 40001 | deadlock o falla de serialización | se reintenta hasta 3 veces; si persiste 503 | `REINTENTAR` |

Forma de todo error: `{"error": {"codigo": "ESPACIO_TOMADO", "mensaje": "texto corto en español"}}`.

## 4. Identidad (Keycloak)

- Realm `medicitas`, importado desde `keycloak/medicitas-realm.json` al arrancar.
- Roles de realm: `admin_plataforma`, `admin_clinica`, `recepcion`, `profesional`.
- Client `medicitas-cli`: público, solo password grant. Lo usan las pruebas, la colección y el demo. Pone `aud = medicitas-api` y `realm_access.roles` en el token.
- Client `medicitas-api`: confidencial, con service account que tiene `manage-users, view-users, query-users, view-realm`. La API lo usa para crear usuarios en Keycloak (RF1, RF2).
- Emisor fijo: `iss = http://localhost:8080/realms/medicitas` (también cuando el token se pide desde dentro de la red). Llaves públicas en `http://keycloak:8080/realms/medicitas/protocol/openid-connect/certs`.
- Validación en la API: `github.com/golang-jwt/jwt/v5` con `github.com/MicahParks/keyfunc/v3` (baja y refresca las llaves del JWKS), algoritmo RS256, verificar firma, `exp`, `iss` y `aud`. Sin token o token inválido: 401. Token válido sin el rol requerido: 403.
- Del token se toma `sub` (identidad) y el rol de dominio (el primero de los 4 que aparezca en `realm_access.roles`). La clínica sale de la base con `resolver_identidad(sub)`. Si el rol del token no coincide con `usuario.rol_codigo`, gana el de la base y se responde 403 si no alcanza.
- Usuarios de prueba (clave para todos: `Medicitas.2026`): `plataforma`; y por cada clínica `admin.<c>`, `recepcion.<c>`, `recepcion2.<c>`, `prof1.<c>` ... `prof4.<c>`, con `<c>` en `central-cartago`, `san-pedro`, `bella-vista`.

## 5. Matriz de permisos aplicada en la API

| Operación | plataforma | admin clínica | recepción | profesional |
|---|---|---|---|---|
| RF1 alta de clínica | sí | no | no | no |
| RF2 a RF4 usuarios, servicios, profesionales | no | sí | no | no |
| RF5, RF6 horarios y bloqueos | no | sí | no | solo los propios |
| RF7, RF8 disponibilidad | no | sí | sí | sí |
| RF9 a RF11 reservar, reprogramar, cancelar | no | sí | sí | no |
| RF12 desenlace | no | sí | sí | sí |
| RF14 pacientes | no | sí | sí | solo lectura |
| RF15 nota clínica | no | no | no | solo de citas propias |
| RF16 historial | no | sí | solo lectura | sí |
| RF19 bitácora | no | sí | no | no |
| RF20 agenda del día, RF21 conteo por estado (ampliación documentada) | no | sí | sí | solo su agenda / no |

La API revisa la matriz primero (403 rápido) y el motor la vuelve a imponer con los perfiles y las políticas. Son dos capas a propósito.

## 6. Rutas (prefijo `/api/v1`, todas con `Authorization: Bearer <token>` salvo `/salud`)

| Método y ruta | RF | Entrada | Salida y códigos |
|---|---|---|---|
| GET `/salud` | | | 200 `{"estado":"ok"}` |
| POST `/plataforma/clinicas` | RF1 | `{codigo, nombre, zona_horaria, admin:{username, nombre, apellidos, correo, clave}}` | 201 `{clinica, admin}`; 409 `DUPLICADO`; 422 |
| GET `/plataforma/clinicas` | RF1 | | 200 lista (sin datos clínicos) |
| GET `/usuarios` · POST `/usuarios` | RF2 | POST `{username, nombre, apellidos, correo, clave, rol}` (rol distinto de admin_plataforma) | 200 / 201; 409; 422 |
| PATCH `/usuarios/{id}` | RF2 | `{rol}` | 200; 404 |
| DELETE `/usuarios/{id}` | RF2 | | 204 baja lógica (activo=false en base y deshabilitado en Keycloak) |
| GET · POST `/especialidades` | RF3 | `{nombre}` | 200 / 201 |
| GET · POST `/servicios` · PATCH `/servicios/{id}` | RF3 | `{nombre, duracion_min, especialidad_id}` · `{nombre?, activo?}` | 200 / 201; 422 |
| GET · POST `/profesionales` | RF4 | `{nombre, especialidad_id, usuario_id?}` | 200 / 201 |
| PUT `/profesionales/{id}/servicios` | RF4 | `{servicio_ids: [...]}` | 200 |
| GET · POST `/profesionales/{id}/horarios` · DELETE `/horarios/{id}` | RF5 | `{dia_semana, hora_inicio "08:00", hora_fin "12:00"}` | 200 / 201 / 204; 409 `HORARIO_SOLAPADO` |
| GET · POST `/profesionales/{id}/bloqueos` · DELETE `/bloqueos/{id}` | RF6 | `{inicio, fin, motivo}` (ISO 8601 con zona) | 201 `{bloqueo, conflictos:[citas vigentes dentro]}` |
| GET `/profesionales/{id}/conflictos` | RF6 | | 200 citas vigentes que caen en bloqueos |
| GET `/disponibilidad?profesional_id&servicio_id&fecha=AAAA-MM-DD` | RF7 | | 200 `{fecha, profesional_id, servicio_id, espacios:[{inicio, fin}], fuente:"cache"|"base"}` |
| GET `/disponibilidad/servicio?servicio_id&desde&hasta` | RF8 | rango máximo 14 días | 200 `{servicio_id, profesionales:[{profesional_id, nombre, dias:[{fecha, espacios}]}]}` |
| POST `/citas` | RF9 | `{paciente_id, profesional_id, servicio_id, inicio}` | 201 concedida; 409 `ESPACIO_TOMADO` o `PACIENTE_OCUPADO`; 422 solicitud inválida |
| GET `/citas/{id}` | | | 200; 404 |
| POST `/citas/{id}/reprogramar` | RF10 | `{inicio, profesional_id?}` | 201 cita nueva (`cita_origen_id` = la vieja); 409; 422 |
| POST `/citas/{id}/cancelar` | RF11 | `{motivo}` | 200; 409 `TRANSICION_INVALIDA` |
| POST `/citas/{id}/desenlace` | RF12 | `{desenlace: "atendida"|"ausente"}` | 200; 409; 422 `DESENLACE_PREMATURO` |
| GET · POST `/pacientes` · GET · PATCH `/pacientes/{id}` | RF14 | `{identificacion, nombre, apellidos, fecha_nacimiento, telefono?, correo?}` | 200 / 201; 409 `DUPLICADO` |
| GET `/pacientes/{id}/historial` | RF16 | | 200 citas con estado |
| POST · GET `/citas/{id}/nota` | RF15 | `{contenido}` | 201 / 200; 403 `NOTA_NO_PERMITIDA` |
| GET `/auditoria?usuario_id&paciente_id&desde&hasta` | RF19 | | 200 |
| GET `/agenda?fecha&profesional_id?` | RF20 | | 200 citas del día ordenadas |
| GET `/reportes/citas-por-estado?desde&hasta` | RF21 | | 200 `{reservada: n, atendida: n, ...}` |

Recurso inexistente o de otra clínica: 404 en ambos casos (no se revela que existe). Cuerpo mal formado: 422. Nunca 500 por entrada del usuario.

Bitácora (RF18): se registra lectura cuando la respuesta incluye teléfono o correo de un paciente (GET `/pacientes/{id}`; en listados se omiten esos campos para no auditar en masa) o el contenido de una nota; escritura en POST/PATCH de paciente que toque contacto y en POST de nota. Va en la misma transacción que la operación.

## 7. Concurrencia (lo que la defensa va a preguntar)

- Nivel: READ COMMITTED, declarado explícitamente en cada transacción. La garantía de I1 e I2 no depende del nivel sino de las restricciones de exclusión: si dos transacciones insertan citas que se solapan, la segunda espera a que la primera termine y, si la primera hizo commit, falla con 23P01. Resultado: exactamente una ganadora.
- Por qué no SERIALIZABLE: también sería correcto, pero aborta transacciones por conflictos de predicado que no son reales y obliga a reintentar todo; con la restricción no hace falta. Por qué no REPEATABLE READ: no evita write skew, solo la restricción lo hace.
- I3 contra bloqueos que se crean al mismo tiempo: candado consultivo por profesional (compartido para reservas, exclusivo para horarios y bloqueos), tomado dentro de los triggers.
- Deadlock: dos recepcionistas intercambian dos citas a la vez (A al espacio de B y B al de A). Postgres lo detecta (40P01), aborta una, la API reintenta hasta 3 veces con espera exponencial y jitter; la reintentada termina en 201 o 409 según lo que quedó.

## 8. Caché de disponibilidad (RF7)

- Clave: `disp:{clinica_id}:{profesional_id}:{fecha}:{servicio_id}:g{gen}:v{ver}` con TTL `CACHE_TTL_SEGUNDOS` (red de seguridad, no el mecanismo de coherencia).
- `ver` = valor de `dispver:{clinica_id}:{profesional_id}:{fecha}` (0 si no existe). `gen` = valor de `dispgen:{clinica_id}:{profesional_id}`.
- Lectura: leer `gen` y `ver`, buscar la clave; si no está, calcular desde la base y guardarla con esa misma `gen`/`ver`.
- Invalidación, siempre después del commit y antes de responder: reservar, cancelar o reprogramar hacen `INCR dispver` de cada (profesional, fecha local) tocado (en reprogramar, el viejo y el nuevo). Crear o borrar horario o bloqueo, y cambiar servicios de un profesional, hacen `INCR dispgen` de ese profesional.
- Por qué versiones y no borrar claves: si un lector calculó con datos viejos y escribe después de la invalidación, escribe bajo la versión vieja, que ya nadie consulta. Borrar claves deja esa ventana abierta.
- Si Redis no responde, se calcula desde la base y se sigue (la base es la verdad).
- RF8 no se cachea en esta etapa (se arma llamando al cálculo de RF7 por día, que sí usa la caché).
