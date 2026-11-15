-- MediCitas · esquema relacional (3FN)
-- Todo objeto lo crea y lo posee medicitas_owner.
SET ROLE medicitas_owner;

-- ---------------------------------------------------------------
-- Funciones de contexto. La API fija estos valores con set_config(..., true)
-- al inicio de cada transacción, a partir del token ya validado.
-- ---------------------------------------------------------------
CREATE FUNCTION app_clinica_actual() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('app.clinica_id', true), '')::uuid
$$;

CREATE FUNCTION app_usuario_actual() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('app.usuario_id', true), '')::uuid
$$;

-- Llave de candado consultivo por profesional (reservas vs. bloqueos/horarios).
CREATE FUNCTION llave_profesional(p uuid) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$
  SELECT hashtextextended('profesional:' || p::text, 0)
$$;

-- ---------------------------------------------------------------
-- Catálogos globales
-- ---------------------------------------------------------------
CREATE TABLE rol (
  codigo      text PRIMARY KEY,
  descripcion text NOT NULL
);

CREATE TYPE estado_cita AS ENUM ('reservada', 'atendida', 'ausente', 'cancelada', 'reprogramada');
CREATE TYPE accion_auditoria AS ENUM ('lectura', 'escritura');
CREATE TYPE recurso_auditoria AS ENUM ('nota_clinica', 'contacto_paciente');

-- ---------------------------------------------------------------
-- Tenant
-- ---------------------------------------------------------------
CREATE TABLE clinica (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  codigo       text NOT NULL UNIQUE CHECK (codigo ~ '^[a-z0-9-]{3,40}$'),
  nombre       text NOT NULL CHECK (length(trim(nombre)) > 0),
  zona_horaria text NOT NULL,
  activa       boolean NOT NULL DEFAULT true,
  creada_en    timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION validar_zona_horaria() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_timezone_names WHERE name = NEW.zona_horaria) THEN
    RAISE EXCEPTION 'Zona horaria inválida: %', NEW.zona_horaria USING ERRCODE = 'MC011';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER clinica_zona_horaria BEFORE INSERT OR UPDATE OF zona_horaria ON clinica
  FOR EACH ROW EXECUTE FUNCTION validar_zona_horaria();

-- ---------------------------------------------------------------
-- Usuarios: solo el identificador del proveedor (sub de Keycloak),
-- la clínica y el rol. Las credenciales viven en Keycloak.
-- ---------------------------------------------------------------
CREATE TABLE usuario (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id     uuid REFERENCES clinica(id),
  idp_sub        uuid NOT NULL UNIQUE,
  rol_codigo     text NOT NULL REFERENCES rol(codigo),
  nombre_visible text NOT NULL,
  activo         boolean NOT NULL DEFAULT true,
  creado_en      timestamptz NOT NULL DEFAULT now(),
  baja_en        timestamptz,
  UNIQUE (clinica_id, id),
  -- El admin de plataforma no pertenece a ninguna clínica; todos los demás a exactamente una.
  CONSTRAINT usuario_clinica_segun_rol CHECK ((rol_codigo = 'admin_plataforma') = (clinica_id IS NULL)),
  CONSTRAINT usuario_baja_coherente CHECK (activo OR baja_en IS NOT NULL)
);

-- ---------------------------------------------------------------
-- Catálogo de la clínica
-- ---------------------------------------------------------------
CREATE TABLE especialidad (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id uuid NOT NULL REFERENCES clinica(id),
  nombre     text NOT NULL,
  UNIQUE (clinica_id, nombre),
  UNIQUE (clinica_id, id)
);

CREATE TABLE servicio (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id      uuid NOT NULL REFERENCES clinica(id),
  nombre          text NOT NULL,
  duracion_min    integer NOT NULL CHECK (duracion_min BETWEEN 5 AND 480 AND duracion_min % 5 = 0),
  especialidad_id uuid NOT NULL,
  activo          boolean NOT NULL DEFAULT true,
  UNIQUE (clinica_id, nombre),
  UNIQUE (clinica_id, id),
  FOREIGN KEY (clinica_id, especialidad_id) REFERENCES especialidad(clinica_id, id)
);

CREATE TABLE profesional (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id      uuid NOT NULL REFERENCES clinica(id),
  usuario_id      uuid UNIQUE,
  nombre          text NOT NULL,
  especialidad_id uuid NOT NULL,
  activo          boolean NOT NULL DEFAULT true,
  UNIQUE (clinica_id, id),
  FOREIGN KEY (clinica_id, especialidad_id) REFERENCES especialidad(clinica_id, id),
  FOREIGN KEY (clinica_id, usuario_id) REFERENCES usuario(clinica_id, id)
);

-- Servicios que cada profesional está habilitado para atender.
CREATE TABLE profesional_servicio (
  clinica_id     uuid NOT NULL REFERENCES clinica(id),
  profesional_id uuid NOT NULL,
  servicio_id    uuid NOT NULL,
  PRIMARY KEY (profesional_id, servicio_id),
  FOREIGN KEY (clinica_id, profesional_id) REFERENCES profesional(clinica_id, id),
  FOREIGN KEY (clinica_id, servicio_id) REFERENCES servicio(clinica_id, id)
);

-- ---------------------------------------------------------------
-- Agenda
-- ---------------------------------------------------------------
-- Horario semanal, varios bloques por día. Horas locales de la clínica.
CREATE TABLE horario_atencion (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id     uuid NOT NULL REFERENCES clinica(id),
  profesional_id uuid NOT NULL,
  dia_semana     smallint NOT NULL CHECK (dia_semana BETWEEN 1 AND 7), -- ISO: 1 = lunes
  hora_inicio    time NOT NULL,
  hora_fin       time NOT NULL,
  CHECK (hora_fin > hora_inicio),
  FOREIGN KEY (clinica_id, profesional_id) REFERENCES profesional(clinica_id, id),
  CONSTRAINT horario_sin_solape EXCLUDE USING gist (
    profesional_id WITH =,
    dia_semana WITH =,
    tsrange(DATE '2000-01-01' + hora_inicio, DATE '2000-01-01' + hora_fin, '[)') WITH &&
  )
);

-- Bloqueos por vacaciones o permisos.
CREATE TABLE bloqueo_agenda (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id     uuid NOT NULL REFERENCES clinica(id),
  profesional_id uuid NOT NULL,
  inicio         timestamptz NOT NULL,
  fin            timestamptz NOT NULL,
  motivo         text NOT NULL,
  creado_por     uuid NOT NULL,
  creado_en      timestamptz NOT NULL DEFAULT now(),
  CHECK (fin > inicio),
  FOREIGN KEY (clinica_id, profesional_id) REFERENCES profesional(clinica_id, id),
  FOREIGN KEY (clinica_id, creado_por) REFERENCES usuario(clinica_id, id)
);

-- ---------------------------------------------------------------
-- Pacientes
-- ---------------------------------------------------------------
CREATE TABLE paciente (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id       uuid NOT NULL REFERENCES clinica(id),
  identificacion   text NOT NULL,
  nombre           text NOT NULL,
  apellidos        text NOT NULL,
  fecha_nacimiento date NOT NULL CHECK (fecha_nacimiento > DATE '1900-01-01'),
  telefono         text,
  correo           text CHECK (correo IS NULL OR correo ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$'),
  creado_en        timestamptz NOT NULL DEFAULT now(),
  actualizado_en   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (clinica_id, identificacion),
  UNIQUE (clinica_id, id)
);

-- ---------------------------------------------------------------
-- Citas
-- fin es derivable (inicio + duración del servicio). Se guarda a propósito:
-- la restricción de exclusión necesita el rango completo en la misma fila.
-- Un trigger lo calcula siempre desde el servicio, nadie lo escribe.
-- ---------------------------------------------------------------
CREATE TABLE cita (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id         uuid NOT NULL REFERENCES clinica(id),
  paciente_id        uuid NOT NULL,
  profesional_id     uuid NOT NULL,
  servicio_id        uuid NOT NULL,
  inicio             timestamptz NOT NULL,
  fin                timestamptz NOT NULL,
  estado             estado_cita NOT NULL DEFAULT 'reservada',
  motivo_cancelacion text,
  cita_origen_id     uuid UNIQUE REFERENCES cita(id),   -- de qué cita viene si fue reprogramada
  creada_por         uuid NOT NULL,
  creada_en          timestamptz NOT NULL DEFAULT now(),
  cerrada_por        uuid,
  cerrada_en         timestamptz,
  UNIQUE (clinica_id, id),
  FOREIGN KEY (clinica_id, paciente_id)    REFERENCES paciente(clinica_id, id),
  FOREIGN KEY (clinica_id, profesional_id) REFERENCES profesional(clinica_id, id),
  FOREIGN KEY (clinica_id, servicio_id)    REFERENCES servicio(clinica_id, id),
  FOREIGN KEY (clinica_id, creada_por)     REFERENCES usuario(clinica_id, id),
  FOREIGN KEY (clinica_id, cerrada_por)    REFERENCES usuario(clinica_id, id),
  CHECK (fin > inicio),
  CONSTRAINT cita_motivo_si_cancelada CHECK ((estado = 'cancelada') = (motivo_cancelacion IS NOT NULL)),
  CONSTRAINT cita_cierre_coherente CHECK ((estado = 'reservada') = (cerrada_en IS NULL)),
  -- Un profesional nunca tiene dos citas vigentes solapadas.
  CONSTRAINT cita_sin_solape_profesional EXCLUDE USING gist (
    profesional_id WITH =, tstzrange(inicio, fin, '[)') WITH &&
  ) WHERE (estado = 'reservada'),
  -- Un paciente nunca tiene dos citas vigentes solapadas (el paciente es de una sola clínica).
  CONSTRAINT cita_sin_solape_paciente EXCLUDE USING gist (
    paciente_id WITH =, tstzrange(inicio, fin, '[)') WITH &&
  ) WHERE (estado = 'reservada')
);

-- Nota clínica, una por cita atendida.
CREATE TABLE nota_clinica (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  clinica_id     uuid NOT NULL REFERENCES clinica(id),
  cita_id        uuid NOT NULL UNIQUE,
  profesional_id uuid NOT NULL,
  contenido      text NOT NULL CHECK (length(trim(contenido)) > 0),
  creada_en      timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (clinica_id, cita_id)        REFERENCES cita(clinica_id, id),
  FOREIGN KEY (clinica_id, profesional_id) REFERENCES profesional(clinica_id, id)
);

-- Bitácora append-only.
CREATE TABLE auditoria (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clinica_id  uuid NOT NULL REFERENCES clinica(id),
  usuario_id  uuid NOT NULL,
  accion      accion_auditoria NOT NULL,
  recurso     recurso_auditoria NOT NULL,
  paciente_id uuid NOT NULL,
  recurso_id  uuid,
  detalle     text,
  ocurrido_en timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (clinica_id, usuario_id)  REFERENCES usuario(clinica_id, id),
  FOREIGN KEY (clinica_id, paciente_id) REFERENCES paciente(clinica_id, id)
);

RESET ROLE;
