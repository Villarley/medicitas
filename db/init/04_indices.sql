-- MediCitas · índices
-- Cada uno se justifica con EXPLAIN (ANALYZE, BUFFERS) en docs/indices.md.
-- Los de las restricciones de exclusión (gist sobre profesional/paciente + rango)
-- ya existen y también sirven a la disponibilidad si la consulta usa tstzrange(...) && ....
SET ROLE medicitas_owner;

-- Disponibilidad: citas vigentes de un profesional en un día.
CREATE INDEX idx_cita_vigente_prof_inicio ON cita (profesional_id, inicio) INCLUDE (fin)
  WHERE estado = 'reservada';

-- Disponibilidad: horario del profesional para un día de la semana.
CREATE INDEX idx_horario_prof_dia ON horario_atencion (profesional_id, dia_semana);

-- Disponibilidad y conflictos: bloqueos que se cruzan con un rango.
CREATE INDEX idx_bloqueo_prof_rango ON bloqueo_agenda USING gist (profesional_id, tstzrange(inicio, fin, '[)'));

-- Agenda del día de la clínica y conteo por estado en un rango.
CREATE INDEX idx_cita_clinica_inicio ON cita (clinica_id, inicio) INCLUDE (estado);

-- Historial del paciente.
CREATE INDEX idx_cita_paciente_inicio ON cita (paciente_id, inicio DESC);

-- Filtros de la bitácora.
CREATE INDEX idx_auditoria_clinica_fecha   ON auditoria (clinica_id, ocurrido_en DESC);
CREATE INDEX idx_auditoria_usuario_fecha   ON auditoria (clinica_id, usuario_id, ocurrido_en DESC);
CREATE INDEX idx_auditoria_paciente_fecha  ON auditoria (clinica_id, paciente_id, ocurrido_en DESC);

-- Llaves foráneas que se usan para filtrar por clínica.
CREATE INDEX idx_profesional_clinica ON profesional (clinica_id);
CREATE INDEX idx_servicio_clinica    ON servicio (clinica_id);
CREATE INDEX idx_paciente_clinica    ON paciente (clinica_id);
CREATE INDEX idx_usuario_clinica     ON usuario (clinica_id);
CREATE INDEX idx_ps_servicio         ON profesional_servicio (servicio_id);

RESET ROLE;
