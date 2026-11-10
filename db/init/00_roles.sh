#!/bin/bash
# Corre una sola vez, cuando el volumen de Postgres está vacío.
# Crea los roles de base de datos. El usuario con el que se conecta la app
# (medicitas_app) no tiene permisos propios: en cada transacción hace
# SET LOCAL ROLE al perfil que le toca según el rol del token.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
     -v app_pass="$APP_DB_PASSWORD" <<'EOSQL'
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Dueño de las tablas. Nadie se conecta con él.
CREATE ROLE medicitas_owner NOLOGIN;
GRANT CREATE, USAGE ON SCHEMA public TO medicitas_owner;

-- Un perfil por actor de la matriz de permisos.
CREATE ROLE db_plataforma    NOLOGIN;
CREATE ROLE db_admin_clinica NOLOGIN;
CREATE ROLE db_recepcion     NOLOGIN;
CREATE ROLE db_profesional   NOLOGIN;

-- Usuario de conexión de la API. NOINHERIT: sin SET ROLE no puede leer nada.
CREATE ROLE medicitas_app LOGIN NOINHERIT PASSWORD :'app_pass';
GRANT db_plataforma, db_admin_clinica, db_recepcion, db_profesional TO medicitas_app;
GRANT USAGE ON SCHEMA public TO db_plataforma, db_admin_clinica, db_recepcion, db_profesional;
EOSQL
