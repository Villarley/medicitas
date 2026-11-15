-- MediCitas · permisos por perfil (menor privilegio) y Row-Level Security (aislamiento entre clínicas)
SET ROLE medicitas_owner;

REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC;
REVOKE ALL ON FUNCTION resolver_identidad(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION resolver_identidad(uuid) TO medicitas_app;

GRANT SELECT ON rol TO db_plataforma, db_admin_clinica, db_recepcion, db_profesional;

-- ---------------------------------------------------------------
-- Perfil 1: plataforma. Alta de clínicas y de su primer admin. Nada clínico.
-- ---------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE (nombre, activa) ON clinica TO db_plataforma;
GRANT SELECT, INSERT ON usuario TO db_plataforma;

-- ---------------------------------------------------------------
-- Perfil 2: admin de clínica. Gestiona catálogo, agenda y citas. Único que lee la bitácora.
-- No lee notas clínicas (matriz).
-- ---------------------------------------------------------------
GRANT SELECT ON clinica TO db_admin_clinica;
GRANT SELECT, INSERT, UPDATE ON usuario, especialidad, servicio, profesional, paciente, cita TO db_admin_clinica;
GRANT SELECT, INSERT, DELETE ON profesional_servicio TO db_admin_clinica;
GRANT SELECT, INSERT, UPDATE, DELETE ON horario_atencion, bloqueo_agenda TO db_admin_clinica;
GRANT SELECT, INSERT ON auditoria TO db_admin_clinica;

-- ---------------------------------------------------------------
-- Perfil 3: recepción. Pacientes y agenda. No toca catálogo, usuarios ni notas.
-- ---------------------------------------------------------------
GRANT SELECT ON clinica, especialidad, servicio, profesional, profesional_servicio,
                horario_atencion, bloqueo_agenda TO db_recepcion;
GRANT SELECT (id, clinica_id, rol_codigo, nombre_visible, activo) ON usuario TO db_recepcion;
GRANT SELECT, INSERT, UPDATE ON paciente, cita TO db_recepcion;
GRANT INSERT ON auditoria TO db_recepcion;

-- ---------------------------------------------------------------
-- Perfil 4: profesional. Lee pacientes, registra desenlace de citas,
-- maneja su propio horario y bloqueos, y escribe/lee sus notas.
-- ---------------------------------------------------------------
GRANT SELECT ON clinica, especialidad, servicio, profesional, profesional_servicio, paciente TO db_profesional;
GRANT SELECT (id, clinica_id, rol_codigo, nombre_visible, activo) ON usuario TO db_profesional;
GRANT SELECT, INSERT, UPDATE, DELETE ON horario_atencion, bloqueo_agenda TO db_profesional;
GRANT SELECT, UPDATE (estado, cerrada_por, cerrada_en) ON cita TO db_profesional;
GRANT SELECT, INSERT ON nota_clinica TO db_profesional;
GRANT INSERT ON auditoria TO db_profesional;

-- La identidad de auditoria necesita USAGE de su secuencia para insertar.
GRANT USAGE ON SEQUENCE auditoria_id_seq TO db_admin_clinica, db_recepcion, db_profesional;

-- Nadie tiene UPDATE ni DELETE sobre auditoria. El trigger lo refuerza
-- incluso para el dueño.

-- ---------------------------------------------------------------
-- Row-Level Security: cada fila solo es visible y escribible
-- dentro de la clínica fijada en app.clinica_id.
-- FORCE para que tampoco el dueño se salte la política.
-- ---------------------------------------------------------------
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['especialidad', 'servicio', 'profesional', 'profesional_servicio',
                           'horario_atencion', 'bloqueo_agenda', 'paciente', 'cita',
                           'nota_clinica', 'auditoria']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY aislamiento_clinica ON %I
                      USING (clinica_id = app_clinica_actual())
                      WITH CHECK (clinica_id = app_clinica_actual())', t);
  END LOOP;
END $$;

-- clinica: la plataforma ve todas; el resto solo la suya.
ALTER TABLE clinica ENABLE ROW LEVEL SECURITY;
ALTER TABLE clinica FORCE ROW LEVEL SECURITY;
CREATE POLICY plataforma_todas ON clinica TO db_plataforma USING (true) WITH CHECK (true);
CREATE POLICY clinica_propia ON clinica TO db_admin_clinica, db_recepcion, db_profesional
  USING (id = app_clinica_actual());
CREATE POLICY resolucion_identidad ON clinica TO medicitas_owner USING (true);

-- usuario: la plataforma solo crea y ve administradores de clínica.
ALTER TABLE usuario ENABLE ROW LEVEL SECURITY;
ALTER TABLE usuario FORCE ROW LEVEL SECURITY;
CREATE POLICY plataforma_admins ON usuario TO db_plataforma
  USING (rol_codigo = 'admin_clinica')
  WITH CHECK (rol_codigo = 'admin_clinica');
CREATE POLICY usuario_de_mi_clinica ON usuario TO db_admin_clinica, db_recepcion, db_profesional
  USING (clinica_id = app_clinica_actual())
  WITH CHECK (clinica_id = app_clinica_actual() AND rol_codigo <> 'admin_plataforma');
CREATE POLICY resolucion_identidad ON usuario TO medicitas_owner USING (true);

-- ---------------------------------------------------------------
-- Políticas restrictivas del profesional ("solo los propios" en la matriz).
-- Se suman (AND) a la de aislamiento.
-- ---------------------------------------------------------------
CREATE FUNCTION mi_profesional() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT p.id FROM profesional p WHERE p.usuario_id = app_usuario_actual()
$$;

CREATE POLICY solo_propios_ins ON horario_atencion AS RESTRICTIVE FOR INSERT TO db_profesional
  WITH CHECK (profesional_id = mi_profesional());
CREATE POLICY solo_propios_upd ON horario_atencion AS RESTRICTIVE FOR UPDATE TO db_profesional
  USING (profesional_id = mi_profesional()) WITH CHECK (profesional_id = mi_profesional());
CREATE POLICY solo_propios_del ON horario_atencion AS RESTRICTIVE FOR DELETE TO db_profesional
  USING (profesional_id = mi_profesional());

CREATE POLICY solo_propios_ins ON bloqueo_agenda AS RESTRICTIVE FOR INSERT TO db_profesional
  WITH CHECK (profesional_id = mi_profesional());
CREATE POLICY solo_propios_upd ON bloqueo_agenda AS RESTRICTIVE FOR UPDATE TO db_profesional
  USING (profesional_id = mi_profesional()) WITH CHECK (profesional_id = mi_profesional());
CREATE POLICY solo_propios_del ON bloqueo_agenda AS RESTRICTIVE FOR DELETE TO db_profesional
  USING (profesional_id = mi_profesional());

-- Desenlace: la matriz dice "sí" para el profesional sin limitarlo a sus citas,
-- así que no se restringe (la matriz no se puede reducir). Lo que sí se limita
-- es qué puede cambiar: solo estado/cierre (GRANT por columna) y solo a
-- atendida o ausente (trigger, MC010).

CREATE POLICY solo_propias ON nota_clinica AS RESTRICTIVE FOR ALL TO db_profesional
  USING (profesional_id = mi_profesional()) WITH CHECK (profesional_id = mi_profesional());

RESET ROLE;
