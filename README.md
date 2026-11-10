# MediCitas

Plataforma SaaS multi-tenant de gestión de citas médicas. Etapa 1: núcleo transaccional (Arquitectura e Ingeniería de Datos, TEC).

> README en construcción: la versión completa (contrato de rutas, usuarios, colección de peticiones) la termina la segunda mitad del proyecto. El contrato técnico vigente está en [docs/CONTRATO.md](docs/CONTRATO.md).

## Arranque

Requisito: Docker con Docker Compose.

```
cp .env.example .env
docker compose up --build
```

Levanta cuatro servicios: `api` (Go), `db` (PostgreSQL 16 con esquema y semilla), `keycloak` (realm importado) y `redis`. La primera vez Keycloak tarda alrededor de un minuto.

## Pruebas

Con la pila arriba:

```
docker compose exec api go test -count=1 ./...
```

Corre las unitarias y las de integración contra la pila real. La prueba de carrera sola:

```
docker compose exec api go test -count=1 -run Carrera -v ./pruebas/
```

## Demo y medición

```
./scripts/demo.sh                              # recorrido de una cita (necesita curl y jq)
docker compose exec api go run ./cmd/medircache  # caché antes y después
```

## Usuarios de prueba

Clave de todos: `Medicitas.2026`. `plataforma`, y por clínica `admin.<c>`, `recepcion.<c>`, `recepcion2.<c>`, `prof1.<c>` a `prof4.<c>`, con `<c>` en `central-cartago`, `san-pedro`, `bella-vista`.

## Documentación

- [docs/CONTRATO.md](docs/CONTRATO.md): decisiones, rutas, errores y matriz de permisos.
- [docs/concurrencia.md](docs/concurrencia.md), [docs/identidad.md](docs/identidad.md), [docs/cache.md](docs/cache.md).
