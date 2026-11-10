-- MediCitas · reglas de negocio garantizadas por el motor (triggers)
-- Códigos de error propios (SQLSTATE clase MC) que la API traduce a HTTP:
--   MC001 fuera del horario de atención (I3)          -> 422
--   MC002 cae dentro de un bloqueo de agenda (I3)      -> 422
--   MC003 transición de estado no permitida (RF13)     -> 409
--   MC004 desenlace antes de la hora de inicio (RF12)  -> 422
--   MC005 nota clínica no permitida (RF15)             -> 403
--   MC006 bitácora inmutable (I6)                      -> 403
--   MC007 campo inmutable de la cita (I4)              -> 422
--   MC008 profesional no habilitado para el servicio   -> 422
--   MC009 profesional o servicio inactivo              -> 422
--   MC010 el profesional solo registra desenlace       -> 403
--   MC011 zona horaria inválida                        -> 422
SET ROLE medicitas_owner;

-- ---------------------------------------------------------------
-- Alta de cita: calcula fin (I4), valida habilitación y horario/bloqueos (I3).
-- El candado compartido por profesional deja correr reservas en paralelo
-- pero espera a cualquier cambio de horario o bloqueo de ese profesional.
-- ---------------------------------------------------------------
CREATE FUNCTION cita_antes_de_insertar() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_duracion integer;
  v_srv_activo boolean;
  v_prof_activo boolean;
  v_tz text;
  v_ini timestamp;
  v_fin timestamp;
BEGIN
  IF NEW.estado <> 'reservada' THEN
    RAISE EXCEPTION 'Una cita nueva solo puede nacer reservada' USING ERRCODE = 'MC003';
  END IF;

  PERFORM pg_advisory_xact_lock_shared(llave_profesional(NEW.profesional_id));

  SELECT s.duracion_min, s.activo INTO v_duracion, v_srv_activo
    FROM servicio s WHERE s.id = NEW.servicio_id AND s.clinica_id = NEW.clinica_id;
  SELECT p.activo INTO v_prof_activo
    FROM profesional p WHERE p.id = NEW.profesional_id AND p.clinica_id = NEW.clinica_id;
  IF v_duracion IS NULL OR v_prof_activo IS NULL THEN
    RAISE EXCEPTION 'Servicio o profesional inexistente' USING ERRCODE = '23503';
  END IF;
  IF NOT v_srv_activo OR NOT v_prof_activo THEN
    RAISE EXCEPTION 'Servicio o profesional inactivo' USING ERRCODE = 'MC009';
  END IF;

  IF NOT EXISTS (SELECT 1 FROM profesional_servicio ps
                  WHERE ps.profesional_id = NEW.profesional_id AND ps.servicio_id = NEW.servicio_id) THEN
    RAISE EXCEPTION 'El profesional no atiende ese servicio' USING ERRCODE = 'MC008';
  END IF;

  -- I4: la duración la manda el servicio, se ignora lo que venga en NEW.fin.
  NEW.fin := NEW.inicio + make_interval(mins => v_duracion);

  -- I3: dentro de un bloque del horario, en hora local de la clínica.
  SELECT c.zona_horaria INTO v_tz FROM clinica c WHERE c.id = NEW.clinica_id;
  v_ini := NEW.inicio AT TIME ZONE v_tz;
  v_fin := NEW.fin AT TIME ZONE v_tz;
  IF v_ini::date <> v_fin::date OR NOT EXISTS (
       SELECT 1 FROM horario_atencion h
        WHERE h.profesional_id = NEW.profesional_id
          AND h.dia_semana = extract(isodow FROM v_ini)
          AND h.hora_inicio <= v_ini::time
          AND h.hora_fin >= v_fin::time) THEN
    RAISE EXCEPTION 'La cita cae fuera del horario de atención' USING ERRCODE = 'MC001';
  END IF;

  IF EXISTS (SELECT 1 FROM bloqueo_agenda b
              WHERE b.profesional_id = NEW.profesional_id
                AND tstzrange(b.inicio, b.fin, '[)') && tstzrange(NEW.inicio, NEW.fin, '[)')) THEN
    RAISE EXCEPTION 'La cita cae dentro de un bloqueo de agenda' USING ERRCODE = 'MC002';
  END IF;

  NEW.cerrada_en := NULL;
  NEW.cerrada_por := NULL;
  RETURN NEW;
END $$;

CREATE TRIGGER cita_insertar BEFORE INSERT ON cita
  FOR EACH ROW EXECUTE FUNCTION cita_antes_de_insertar();

-- ---------------------------------------------------------------
-- Cambio de cita: máquina de estados (RF13), desenlace (RF12) e I4.
-- reservada -> atendida | ausente | cancelada | reprogramada. Los finales no salen.
-- ---------------------------------------------------------------
CREATE FUNCTION cita_antes_de_actualizar() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.clinica_id <> OLD.clinica_id OR NEW.paciente_id <> OLD.paciente_id
     OR NEW.profesional_id <> OLD.profesional_id OR NEW.servicio_id <> OLD.servicio_id
     OR NEW.inicio <> OLD.inicio OR NEW.fin <> OLD.fin
     OR NEW.creada_por <> OLD.creada_por OR NEW.creada_en <> OLD.creada_en
     OR NEW.cita_origen_id IS DISTINCT FROM OLD.cita_origen_id THEN
    RAISE EXCEPTION 'Esos campos de la cita no se modifican; para mover una cita se reprograma'
      USING ERRCODE = 'MC007';
  END IF;

  IF OLD.estado <> 'reservada' THEN
    RAISE EXCEPTION 'La cita ya está en estado final (%)', OLD.estado USING ERRCODE = 'MC003';
  END IF;
  IF NEW.estado = 'reservada' THEN
    RAISE EXCEPTION 'Transición vacía: la cita sigue reservada' USING ERRCODE = 'MC003';
  END IF;

  IF current_user = 'db_profesional' AND NEW.estado NOT IN ('atendida', 'ausente') THEN
    RAISE EXCEPTION 'El profesional solo registra el desenlace' USING ERRCODE = 'MC010';
  END IF;

  IF NEW.estado IN ('atendida', 'ausente') AND now() < OLD.inicio THEN
    RAISE EXCEPTION 'El desenlace solo se registra después de la hora de inicio' USING ERRCODE = 'MC004';
  END IF;

  NEW.cerrada_en := now();
  NEW.cerrada_por := coalesce(app_usuario_actual(), NEW.cerrada_por);
  RETURN NEW;
END $$;

CREATE TRIGGER cita_actualizar BEFORE UPDATE ON cita
  FOR EACH ROW EXECUTE FUNCTION cita_antes_de_actualizar();

CREATE FUNCTION cita_no_se_borra() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'Las citas no se borran; se cancelan' USING ERRCODE = 'MC003';
END $$;

CREATE TRIGGER cita_borrar BEFORE DELETE ON cita
  FOR EACH ROW EXECUTE FUNCTION cita_no_se_borra();

-- ---------------------------------------------------------------
-- Cambios de horario y bloqueos: candado exclusivo por profesional,
-- así no se cruzan con una reserva que está validando I3.
-- ---------------------------------------------------------------
CREATE FUNCTION agenda_candado_exclusivo() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP IN ('UPDATE', 'DELETE') THEN
    PERFORM pg_advisory_xact_lock(llave_profesional(OLD.profesional_id));
  END IF;
  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    PERFORM pg_advisory_xact_lock(llave_profesional(NEW.profesional_id));
    RETURN NEW;
  END IF;
  RETURN OLD;
END $$;

CREATE TRIGGER horario_candado BEFORE INSERT OR UPDATE OR DELETE ON horario_atencion
  FOR EACH ROW EXECUTE FUNCTION agenda_candado_exclusivo();
CREATE TRIGGER bloqueo_candado BEFORE INSERT OR UPDATE OR DELETE ON bloqueo_agenda
  FOR EACH ROW EXECUTE FUNCTION agenda_candado_exclusivo();

-- ---------------------------------------------------------------
-- Nota clínica (RF15): solo sobre cita atendida y solo el profesional que la atendió.
-- ---------------------------------------------------------------
CREATE FUNCTION nota_antes_de_insertar() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_estado estado_cita;
  v_prof uuid;
  v_usuario_prof uuid;
BEGIN
  SELECT c.estado, c.profesional_id INTO v_estado, v_prof
    FROM cita c WHERE c.id = NEW.cita_id AND c.clinica_id = NEW.clinica_id;
  IF v_estado IS NULL THEN
    RAISE EXCEPTION 'Cita inexistente' USING ERRCODE = '23503';
  END IF;
  IF v_estado <> 'atendida' THEN
    RAISE EXCEPTION 'Solo se escribe nota sobre una cita atendida' USING ERRCODE = 'MC005';
  END IF;
  SELECT p.usuario_id INTO v_usuario_prof FROM profesional p WHERE p.id = v_prof;
  IF NEW.profesional_id <> v_prof OR v_usuario_prof IS DISTINCT FROM app_usuario_actual() THEN
    RAISE EXCEPTION 'Solo el profesional que atendió escribe la nota' USING ERRCODE = 'MC005';
  END IF;
  NEW.creada_en := now();
  RETURN NEW;
END $$;

CREATE TRIGGER nota_insertar BEFORE INSERT ON nota_clinica
  FOR EACH ROW EXECUTE FUNCTION nota_antes_de_insertar();

-- ---------------------------------------------------------------
-- Bitácora (I6): quién y cuándo los pone el motor; no se edita ni se borra.
-- ---------------------------------------------------------------
CREATE FUNCTION auditoria_antes_de_insertar() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF app_usuario_actual() IS NOT NULL THEN
    NEW.usuario_id := app_usuario_actual();
  END IF;
  NEW.ocurrido_en := now();
  RETURN NEW;
END $$;

CREATE TRIGGER auditoria_insertar BEFORE INSERT ON auditoria
  FOR EACH ROW EXECUTE FUNCTION auditoria_antes_de_insertar();

CREATE FUNCTION auditoria_inmutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'La bitácora de auditoría es append-only' USING ERRCODE = 'MC006';
END $$;

CREATE TRIGGER auditoria_sin_cambios BEFORE UPDATE OR DELETE ON auditoria
  FOR EACH ROW EXECUTE FUNCTION auditoria_inmutable();
CREATE TRIGGER auditoria_sin_truncate BEFORE TRUNCATE ON auditoria
  FOR EACH STATEMENT EXECUTE FUNCTION auditoria_inmutable();

-- ---------------------------------------------------------------
-- Resolución de identidad: sub del token -> usuario, clínica y rol.
-- SECURITY DEFINER porque se llama antes de saber la clínica (RLS aún sin contexto).
-- Solo devuelve la fila de ese sub, nada más.
-- ---------------------------------------------------------------
CREATE FUNCTION resolver_identidad(p_sub uuid)
RETURNS TABLE (usuario_id uuid, clinica_id uuid, rol_codigo text, activo boolean, clinica_activa boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.id, u.clinica_id, u.rol_codigo, u.activo, coalesce(c.activa, true)
    FROM usuario u LEFT JOIN clinica c ON c.id = u.clinica_id
   WHERE u.idp_sub = p_sub
$$;

RESET ROLE;
