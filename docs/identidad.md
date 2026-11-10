# Identidad delegada (Keycloak)

## Qué hace Keycloak y qué hacemos nosotros

Keycloak autentica, emite y firma los tokens. Nosotros no escribimos inicio de sesión, ni guardamos contraseñas, ni firmamos nada. La API solo valida lo que recibe.

El realm se crea solo al arrancar: Compose monta `keycloak/medicitas-realm.json` y Keycloak lo importa con `--import-realm`. Ese archivo se genera con `scripts/generar_semilla.py` a partir de `keycloak/realm-base.json`.

| Pieza | Para qué |
|---|---|
| Realm `medicitas` | Espacio de usuarios de la plataforma |
| Roles de realm `admin_plataforma`, `admin_clinica`, `recepcion`, `profesional` | Los cuatro actores de la matriz |
| Client `medicitas-cli` (público, password grant) | Lo usan las pruebas, el demo y la colección de peticiones. Agrega `aud = medicitas-api` y los roles al token |
| Client `medicitas-api` (confidencial, cuenta de servicio) | La API lo usa para crear y deshabilitar usuarios en Keycloak (RF1, RF2) por la Admin REST API |

## Validación del token

En `internal/auth/auth.go`:

1. `keyfunc` baja las llaves públicas del JWKS de Keycloak (`OIDC_JWKS_URL`) y las vuelve a pedir cuando aparece un `kid` nuevo, así una rotación de llaves no requiere reiniciar la API.
2. `golang-jwt` verifica la firma con esa llave, exige RS256 (rechaza `none` y HS256), y revisa `exp`, `iss` y `aud`.
3. Sin token o con cualquier falla de la validación: `401`.

El emisor queda fijo en `http://localhost:8080/realms/medicitas` con `KC_HOSTNAME`, aunque la API baje las llaves por `keycloak:8080` dentro de la red de Compose. Sin eso, un token pedido desde la laptop y uno pedido desde un contenedor tendrían emisores distintos.

`TestIdentidad` prueba: sin token, firma alterada, token firmado con una llave RSA que no es de Keycloak y basura, todo 401. Profesional reservando y plataforma consultando agendas, 403. Los tres roles de clínica consultando disponibilidad, 200.

## Cómo se asocia cada identidad con su clínica

Del token solo tomamos dos cosas: el `sub` y los roles. Con el `sub` llamamos a `resolver_identidad(sub)` en PostgreSQL, que devuelve el usuario, su clínica y su rol según la tabla `usuario` (`usuario.idp_sub` es el `sub` de Keycloak).

- Si no existe, está dado de baja o su clínica está inactiva: `403`.
- El token tiene que traer el mismo rol que la base. Si no coinciden: `403`.
- La clínica nunca se lee de la petición. Si un cliente manda `clinica_id` en la URL o en el cuerpo, se ignora (`TestFugaEnCitasYDisponibilidad` lo prueba).

Por qué no ponemos la clínica como claim del token: cualquiera con acceso a la consola de Keycloak podría cambiarla sin que la base se entere, y de ese dato depende I5. La base es la fuente de verdad del tenant; Keycloak, de la identidad.

`resolver_identidad` es la única función `SECURITY DEFINER` del esquema, porque se llama antes de saber la clínica (cuando RLS todavía no tiene contexto). Solo devuelve la fila de ese `sub`.

## De rol de token a permisos

Dos capas a propósito:

1. La API: `RequiereRoles` en cada ruta, según la sección 5 del contrato. Responde 403 rápido.
2. El motor: cada transacción hace `SET LOCAL ROLE` al perfil de base del rol (`db_admin_clinica`, `db_recepcion`, `db_profesional`, `db_plataforma`), con sus permisos y políticas RLS. El nombre del perfil sale de un mapa fijo en `internal/db/db.go`, nunca del texto del token.

## Por qué no gRPC en esta etapa

Lo consideramos. El enunciado pide una API REST y exactamente cuatro servicios en Compose (aplicación, motor relacional, Keycloak y Redis), así que un servicio gRPC aparte no cabe. Generar REST desde un `.proto` con grpc-gateway sí cabría, pero suma generación de código y otra capa que defender sin cambiar lo que se evalúa. Lo dejamos para la Etapa 2, cuando el sistema se separe en servicios que sí necesitan hablar entre ellos.
