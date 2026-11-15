# MediCitas

Plataforma SaaS multi-tenant de gestión de citas médicas: una empresa la vende a muchas clínicas a la vez y los datos de cada clínica quedan estrictamente aislados de las demás.

## Arranque

Requisito: Docker con Docker Compose.

```
cp .env.example .env
docker compose up --build
```

Levanta cuatro servicios y deja el sistema listo:

| Servicio | Puerto | Qué hace al arrancar |
|---|---|---|
| `db` (PostgreSQL 16) | 5432 | Corre `db/init/`: roles, esquema, reglas, permisos y RLS, índices y semilla |
| `keycloak` | 8080 | Importa el realm `medicitas` con roles, clients y usuarios de prueba |
| `redis` | interno | Caché de disponibilidad |
| `api` (Go) | 8000 | Espera a los otros tres sanos y sirve `/api/v1` |

La primera vez Keycloak tarda alrededor de un minuto. Listo cuando `curl localhost:8000/api/v1/salud` responde `{"estado":"ok"}`. Los servicios se hablan por nombre (`db`, `redis`, `keycloak`) dentro de la red de Compose.

Para empezar de cero (base y realm nuevos): `docker compose down -v && docker compose up --build`.

## Pruebas

Con el stack arriba, un solo comando corre las unitarias y las de integración contra el stack real:

```
docker compose exec api go test -count=1 ./...
```

No hace falta preparar datos. La prueba de carrera sola, con detalle:

```
docker compose exec api go test -count=1 -run Carrera -v ./pruebas/
```

Qué prueba cubre cada requisito e invariante: [docs/pruebas.md](docs/pruebas.md).

## Usuarios de prueba

Clave de todos: `Medicitas.2026` (para dev).

| Usuario | Rol |
|---|---|
| `plataforma` | admin de plataforma (sin clínica) |
| `admin.<c>` | admin de clínica |
| `recepcion.<c>`, `recepcion2.<c>` | recepción |
| `prof1.<c>` a `prof4.<c>` | profesional |

con `<c>` en `central-cartago`, `san-pedro`, `bella-vista`.

Pedir un token:

```
curl -s -d grant_type=password -d client_id=medicitas-cli -d username=recepcion.central-cartago \
     -d password=Medicitas.2026 http://localhost:8080/realms/medicitas/protocol/openid-connect/token | jq -r .access_token
```

## Contrato de la API

Prefijo `/api/v1`. Todas las rutas salvo `/salud` exigen `Authorization: Bearer <token de Keycloak>`. Sin token o con token inválido: 401. Con token válido sin el rol requerido: 403. La clínica sale siempre de la identidad; un `clinica_id` en la petición se ignora.

Forma de todo error: `{"error": {"codigo": "ESPACIO_TOMADO", "mensaje": "texto corto"}}`. Un recurso inexistente o de otra clínica responde 404 en ambos casos. Un cuerpo mal formado o un dato inválido responde 422. Ninguna entrada del usuario produce 500.

Roles: P = plataforma, A = admin de clínica, R = recepción, Pr = profesional.

### Plataforma y clínica

| Método y ruta | RF | Roles | Entrada | Salida y códigos |
|---|---|---|---|---|
| GET `/salud` | | público | | 200 `{"estado":"ok"}` |
| GET `/plataforma/clinicas` | RF1 | P | | 200 lista de clínicas (sin datos clínicos) |
| POST `/plataforma/clinicas` | RF1 | P | `{codigo, nombre, zona_horaria, admin:{username, nombre, apellidos, correo, clave}}` | 201 `{clinica, admin}`; 409 `DUPLICADO`; 422; 502 `PROVEEDOR_IDENTIDAD` |
| GET `/usuarios?activo=` | RF2 | A | | 200 lista |
| POST `/usuarios` | RF2 | A | `{username, nombre, apellidos, correo, clave, rol}` (rol: `admin_clinica`, `recepcion` o `profesional`) | 201; 409 `DUPLICADO`; 422 |
| PATCH `/usuarios/{id}` | RF2 | A | `{rol}` | 200; 404; 409 `AUTOGESTION` (sobre uno mismo) o `USUARIO_INACTIVO` |
| DELETE `/usuarios/{id}` | RF2 | A | | 204 baja lógica (inactivo en la base y deshabilitado en Keycloak; idempotente); 404; 409 `AUTOGESTION` |

### Catálogo

| Método y ruta | RF | Roles | Entrada | Salida y códigos |
|---|---|---|---|---|
| GET `/especialidades` | RF3 | A R Pr | | 200 |
| POST `/especialidades` | RF3 | A | `{nombre}` | 201; 409 `DUPLICADO` |
| GET `/servicios?activo=` | RF3 | A R Pr | | 200 |
| POST `/servicios` | RF3 | A | `{nombre, duracion_min, especialidad_id}` (5 a 480, múltiplo de 5) | 201; 404 especialidad; 409; 422 |
| PATCH `/servicios/{id}` | RF3 | A | `{nombre?, activo?}` | 200; 404; 422 `CAMPO_INMUTABLE` si trae `duracion_min` (I4) |
| GET `/profesionales?servicio_id=` | RF4 | A R Pr | | 200 con `servicio_ids` |
| GET `/profesionales/{id}` | RF4 | A R Pr | | 200; 404 |
| POST `/profesionales` | RF4 | A | `{nombre, especialidad_id, usuario_id?}` (usuario activo con rol profesional) | 201; 404; 409; 422 |
| PUT `/profesionales/{id}/servicios` | RF4 | A | `{servicio_ids: [...]}` (reemplaza el conjunto) | 200; 404; 422 `ESPECIALIDAD_NO_COINCIDE` |

### Agenda

| Método y ruta | RF | Roles | Entrada | Salida y códigos |
|---|---|---|---|---|
| GET `/profesionales/{id}/horarios` | RF5 | A, Pr (propio) | | 200; 403; 404 |
| POST `/profesionales/{id}/horarios` | RF5 | A, Pr (propio) | `{dia_semana (1 lunes..7), hora_inicio "08:00", hora_fin "12:00"}` | 201; 409 `HORARIO_SOLAPADO`; 422 |
| DELETE `/horarios/{id}` | RF5 | A, Pr (propio) | | 204; 404; 409 `HORARIO_CON_CITAS` (I3) |
| GET `/profesionales/{id}/bloqueos?desde&hasta` | RF6 | A, Pr (propio) | | 200 |
| POST `/profesionales/{id}/bloqueos` | RF6 | A, Pr (propio) | `{inicio, fin, motivo}` (ISO 8601 con zona) | 201 `{bloqueo, conflictos:[citas vigentes dentro]}`; 422 |
| DELETE `/bloqueos/{id}` | RF6 | A, Pr (propio) | | 204; 404 |
| GET `/profesionales/{id}/conflictos` | RF6 | A R, Pr (propio) | | 200 citas vigentes que caen en bloqueos |
| GET `/disponibilidad?profesional_id&servicio_id&fecha=AAAA-MM-DD` | RF7 | A R Pr | | 200 `{fecha, profesional_id, servicio_id, espacios:[{inicio, fin}], fuente:"cache"\|"base"}` |
| GET `/disponibilidad/servicio?servicio_id&desde&hasta` | RF8 | A R Pr | rango máximo 14 días | 200 `{servicio_id, profesionales:[{profesional_id, nombre, dias:[{fecha, espacios}]}]}` |
| GET `/agenda?fecha&profesional_id?` | RF20 | A R, Pr (la suya) | | 200 citas del día ordenadas por hora, con nombres |
| GET `/reportes/citas-por-estado?desde&hasta` | RF21 | A R | fechas AAAA-MM-DD, `hasta` incluido | 200 `{reservada, atendida, ausente, cancelada, reprogramada}` |

### Citas

| Método y ruta | RF | Roles | Entrada | Salida y códigos |
|---|---|---|---|---|
| POST `/citas` | RF9 | A R | `{paciente_id, profesional_id, servicio_id, inicio}` | **201 concedida**; **409 `ESPACIO_TOMADO`** o `PACIENTE_OCUPADO` (espacio ya tomado); **422** o 404 (solicitud inválida: `FUERA_DE_HORARIO`, `EN_BLOQUEO`, `SERVICIO_NO_HABILITADO`, `INACTIVO`, ...) |
| GET `/citas/{id}` | | A R Pr | | 200; 404 |
| POST `/citas/{id}/reprogramar` | RF10 | A R | `{inicio, profesional_id?}` | 201 cita nueva (`cita_origen_id` = la vieja); 409; 422 |
| POST `/citas/{id}/cancelar` | RF11 | A R | `{motivo}` | 200; 409 `TRANSICION_INVALIDA` |
| POST `/citas/{id}/desenlace` | RF12 | A R Pr | `{desenlace: "atendida"\|"ausente"}` | 200; 409; 422 `DESENLACE_PREMATURO` |
| POST `/citas/{id}/nota` | RF15 | Pr (quien atendió) | `{contenido}` | 201; 403 `NOTA_NO_PERMITIDA`; 409 si ya tiene |
| GET `/citas/{id}/nota` | RF15 | Pr (quien atendió) | | 200; 404 |

### Pacientes y bitácora

| Método y ruta | RF | Roles | Entrada | Salida y códigos |
|---|---|---|---|---|
| GET `/pacientes?buscar&limite&desplazamiento` | RF14 | A R Pr | `buscar`: identificación exacta o parte del nombre | 200 **sin** datos de contacto |
| POST `/pacientes` | RF14 | A R | `{identificacion, nombre, apellidos, fecha_nacimiento, telefono?, correo?}` | 201; 409 `DUPLICADO`; 422 |
| GET `/pacientes/{id}` | RF14 | A R Pr | | 200 con contacto (se audita); 404 |
| PATCH `/pacientes/{id}` | RF14 | A R | campos opcionales; `""` borra teléfono o correo | 200; 404; 422 |
| GET `/pacientes/{id}/historial` | RF16 | A R Pr | | 200 citas con estado, la más reciente primero |
| GET `/auditoria?usuario_id&paciente_id&desde&hasta&limite` | RF19 | A | | 200 |

Bitácora (RF18): se registra la lectura y la escritura de datos de contacto (`GET`, `POST`, `PATCH` de un paciente) y de notas clínicas, en la misma transacción que la operación. Detalle en [docs/seguridad.md](docs/seguridad.md).

## Documentación

| Documento | Contenido |
|---|---|
| [docs/diseno.md](docs/diseno.md) | Diagrama ER, diccionario de datos, 3FN paso a paso, desnormalizaciones |
| [docs/multitenancy.md](docs/multitenancy.md) | Comparación de estrategias, la elegida y sus trade-offs, evidencia de I5 |
| [docs/concurrencia.md](docs/concurrencia.md) | La pregunta rectora, nivel de aislamiento, deadlock |
| [docs/identidad.md](docs/identidad.md) | Keycloak, validación del token, cómo se asocia la identidad con la clínica |
| [docs/cache.md](docs/cache.md) | Clave, invalidación por versiones, medición antes y después |
| [docs/seguridad.md](docs/seguridad.md) | Roles de base, matriz en el motor, parametrización, bitácora |
| [docs/indices.md](docs/indices.md) | Cada índice con su plan de ejecución |
| [docs/pruebas.md](docs/pruebas.md) | Informe de pruebas y trazabilidad a RF, invariantes y criterios |
| [docs/CONTRATO.md](docs/CONTRATO.md) | Contrato técnico interno del grupo |
| [docs/declaracion-ia.md](docs/declaracion-ia.md) | Declaración de uso de inteligencia artificial |

## Estructura

```
docker-compose.yml, .env.example
db/init/        00_roles.sh, 01_esquema.sql, 02_reglas.sql, 03_seguridad.sql, 04_indices.sql, 05_semilla.sql (generado)
keycloak/       realm-base.json, medicitas-realm.json (generado)
scripts/        generar_semilla.py, demo.sh, explicar_indices.sql
peticiones/     medicitas.http
api/            cmd/api, cmd/medircache, internal/{auth,cache,citas,config,db,disponibilidad,errores,http,identidad,keycloak}, pruebas/
docs/
```

Los datos semilla se regeneran con `python3 scripts/generar_semilla.py` (determinista).
