# Declaración de uso de inteligencia artificial

## Herramientas usadas

- Claude (Anthropic), usando Claude Code como harness, con acceso al repositorio y al stack de Docker Compose local. Mayoritariamente se utilizó Opus 5.5, Sonnet 5.5 y probamos Zed (IDE) usando Grok 4.7 con el Harness de Cursor.

## Reparto del trabajo en el grupo

- Santiago: infraestructura (Docker Compose, Postgres, Redis, Keycloak), esquema y reglas en el motor, seguridad por perfil y RLS, índices, autenticación, caché y las rutas de RF7 a RF13 con sus pruebas, más `docs/concurrencia.md`, `identidad.md` y `cache.md`.
- Fabián: rutas de RF1 a RF6, RF14 a RF16 y RF18 a RF21, sus pruebas, `docs/diseno.md`, `multitenancy.md`, `indices.md`, `seguridad.md`, `pruebas.md`, el README completo y la colección de peticiones.

## Qué hizo la herramienta

En distintas sesiones de Claude Code, a partir del enunciado, la herramienta:

1. Comparó el enunciado contra el repositorio y listó lo que faltaba.
2. Ayudó a escribir las rutas de RF1 a RF6, RF14 a RF16 y RF18 a RF21 (`api/internal/http/{ayudas,catalogo,agenda,pacientes,plataforma}.go` y el registro en `rutas_gestion.go`), siguiendo el patrón que ya tenía el código de citas y disponibilidad.
3. Ayudó a escribir las pruebas de integración de esas rutas (`api/pruebas/{plataforma,catalogo,agenda,pacientes,aislamiento_gestion,helpers_gestion}_test.go`) y las unitarias (`api/internal/http/ayudas_test.go`).
4. Capturó los planes de ejecución, encontró que la consulta de citas de RF7 no usaba bien su índice y la reescribió (`api/internal/disponibilidad/servicio.go`).
5. Facilitó redactar `docs/diseno.md`, `multitenancy.md`, `pruebas.md`, el README y la colección `peticiones/medicitas.http`.

Decisiones de diseño que propuso la herramienta:

- No dejar borrar un bloque de horario con citas vigentes dentro (409 `HORARIO_CON_CITAS`). El DELETE va antes del conteo para aprovechar el candado del trigger.
- Exigir que un profesional solo atienda servicios de su especialidad (validación en la API, no en el motor)..
- Reescribir el solape de RF7 como rango sobre `inicio` con la cota de 480 minutos.

## Qué verificó la herramienta

- Una prueba de mutación: quitó el registro de bitácora de `GET /pacientes/{id}` y comprobó que `TestBitacoraDeContacto` falla.
- La protección por triggers de la bitácora, a mano como superusuario.
