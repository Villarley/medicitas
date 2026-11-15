# Multi-tenancy

El invariante importante es que ningún dato de una clínica resulta accesible desde otra, por ninguna ruta ni combinación de parámetros.

## Se compararon 3 estrategias

| | Esquema por tenant | Columna discriminadora (filtro en la app) | Columna discriminadora + Row-Level Security |
|---|---|---|---|
| Cómo funciona | Cada clínica tiene su propio esquema (`clinica_a.cita`, `clinica_b.cita`); la conexión fija `search_path` | Todas las clínicas en las mismas tablas, con `clinica_id`; cada consulta de la app agrega `WHERE clinica_id = $x` | Igual que la anterior, pero el motor agrega el filtro con políticas RLS a partir de una variable de sesión |
| Quién garantiza el aislamiento | El catálogo de PostgreSQL (si el `search_path` es correcto) | El programador, en cada consulta | El motor, en cada fila, para cualquier consulta |
| Un olvido en el código | Un `search_path` mal fijado expone una clínica entera | Un `WHERE` olvidado expone todas las clínicas | No expone nada: la política se aplica igual |
| Alta de una clínica (RF1) | DDL: crear esquema, tablas, índices, permisos. Lento y con más riesgo | Un INSERT | Un INSERT |
| Cambios de esquema | Migrar N esquemas | Una vez | Una vez |
| Consultas entre clínicas (plataforma, Etapa 2 analítica) | UNION de N esquemas | Directas | Directas con un perfil que tenga una política propia |
| Índices y planes | Uno por clínica, chicos | Compartidos; conviene que empiecen por `clinica_id` | Igual; el predicado de RLS entra en la condición del índice (ver abajo) |
| Costo con muchas clínicas | Miles de esquemas: catálogo grande, conexiones por esquema | Bajo | Bajo, más una evaluación de política por fila |
| "Vecino ruidoso" | Aislado a nivel lógico, no de recursos | Compartido | Compartido |

## Lo que elegimos fue columna discriminadora + RLS

Elegimos la tercera porque es la única en la que un error en la aplicación no se convierte en fuga. Un SaaS clínico vive de esa garantía, y el alta de clínicas tiene que ser un INSERT, no una migración. Ninguna capa confía en la anterior:

1. **La clínica sale de la identidad, no de la petición.** El middleware valida el token, toma el `sub` y busca en la base el usuario, su clínica y su rol (`resolver_identidad`). Un `clinica_id` en la URL o en el cuerpo se ignora. Ver `docs/identidad.md`.
2. **La transacción fija el contexto.** `EnTransaccion` hace `SET LOCAL ROLE <perfil>` y `set_config('app.clinica_id', ..., true)`. Las dos cosas son locales a la transacción y desaparecen al terminar, así que una conexión reutilizada del pool no arrastra la clínica anterior.
3. **RLS con `FORCE`.** Las 10 tablas de tenant tienen la política `aislamiento_clinica`: `USING (clinica_id = app_clinica_actual()) WITH CHECK (...)`. `FORCE ROW LEVEL SECURITY` hace que tampoco el dueño de las tablas se la salte. Leer lo ajeno da 0 filas; escribir con otro `clinica_id` da error. `clinica` y `usuario` tienen políticas propias (la plataforma ve clínicas y admins, nada clínico).
4. **Llaves foráneas compuestas.** `(clinica_id, paciente_id) REFERENCES paciente(clinica_id, id)`, y así en todas. Ni siquiera con RLS apagado puede existir una cita de la clínica A con un paciente de la B.
5. **La caché lleva la clínica en la clave** (`disp:{clinica_id}:...`), tomada de la identidad. Una clínica no puede leer entradas de otra.
6. **404, no 403.** Un id de otra clínica responde igual que un id inexistente, así no se puede ni confirmar que existe.

La app además escribe sus consultas sin `WHERE clinica_id`: dejamos que RLS lo haga, para que la prueba de aislamiento esté probando el motor y no nuestra disciplina. El costo es que el aislamiento depende de que `app.clinica_id` esté bien puesto, y eso pasa en un solo lugar (`internal/db/db.go`), con el valor que viene de la base.

### Qué sacrificamos

- **Recursos compartidos.** Una clínica con mucha carga afecta a las demás. Es un problema de la Etapa 2 (particionar por `clinica_id`, réplicas), y la columna discriminadora lo deja fácil: `PARTITION BY HASH (clinica_id)` usa la misma columna.
- **Un error en una política afecta a todas las clínicas a la vez.** Por eso las políticas son pocas, iguales en las 10 tablas (se crean en un bucle en `03_seguridad.sql`) y las prueban `TestFugaEnCitasYDisponibilidad` y `TestFugaEnRutasDeGestion`.
- **Un superusuario se salta RLS.** La API nunca se conecta como superusuario: usa `medicitas_app`, que es `NOINHERIT` y sin permisos propios. Sin el `SET ROLE` no puede leer ni una tabla.
- **Costo por fila.** La política se evalúa en cada fila. En la práctica el planificador la empuja al índice: en el plan de la agenda del día (RF20), `clinica_id = app_clinica_actual()` aparece como `Index Cond` de `idx_cita_clinica_inicio`, no como filtro posterior (ver `docs/indices.md`). Por eso los índices de listados empiezan por `clinica_id`.

### Por qué no esquema por tenant

Aísla bien a nivel lógico, pero el alta de una clínica pasaría a ser DDL en caliente, cada cambio de esquema se multiplica por el número de clínicas y la analítica de la Etapa 2 tendría que unir N esquemas. Además el aislamiento dependería del `search_path`, que es justamente un parámetro de sesión que un error de la app puede dejar mal puesto. Si lo eligiéramos tendríamos que poner RLS de todas formas para estar tranquilos.

### Por qué no solo columna discriminadora

Es lo más simple, pero convierte cada consulta en un punto de falla. Las rutas de listado con filtros (`/pacientes?buscar=`, `/auditoria?paciente_id=`, `/agenda?profesional_id=`) son justo donde un `WHERE` olvidado o mal armado fuga datos, y el enunciado pide resistir intentos deliberados ahí.

## Evidencia

| Prueba | Qué intenta |
|---|---|
| `TestFugaEnCitasYDisponibilidad` | Leer, cancelar y reprogramar una cita de San Pedro desde Central; reservar con paciente o profesional ajeno; mandar `clinica_id` ajeno en query y cuerpo; consultar disponibilidad ajena |
| `TestFugaEnRutasDeGestion` | Con los tres roles de clínica: leer paciente, historial, profesional, horarios, bloqueos, conflictos, agenda y nota ajenos por id; modificar paciente, servicio, servicios del profesional, horario, bloqueo y usuario ajenos; crear servicio o profesional que referencien especialidad o usuario ajeno; listados con `clinica_id` manipulado; bitácora filtrada por paciente ajeno; agenda que incluya citas ajenas. Al final verifica en la base de San Pedro que nada cambió |
| `TestAltaDeClinica` | Una clínica recién creada empieza vacía: su admin no ve pacientes, servicios, profesionales ni usuarios de las otras. La plataforma no ve datos clínicos (403) |
| `TestIdentidad` | Token firmado por otra llave con roles inventados: 401 |
