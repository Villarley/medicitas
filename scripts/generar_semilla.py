"""Genera los datos semilla de MediCitas.

Salidas (se versionan en el repo):
  db/init/05_semilla.sql        catálogo, usuarios, horarios, pacientes + bloque que crea citas
  keycloak/medicitas-realm.json realm con roles, clients y los mismos usuarios (mismo sub)

Uso:  python scripts/generar_semilla.py
Es determinista: los UUID salen de uuid5 sobre un nombre fijo, así que correrlo
dos veces produce los mismos archivos.

Las citas no se escriben con fecha fija: el bloque DO del final las crea
alrededor de la fecha en que se levanta la base (45 días atrás, 21 adelante),
para que siempre haya pasado y futuro.
"""
import json
import random
import uuid
from pathlib import Path

RAIZ = Path(__file__).resolve().parent.parent
NS = uuid.UUID("6f1c8d2e-0b5a-4c1e-9a7d-3e2f1b0c4d5a")
CLAVE_PRUEBA = "Medicitas.2026"   # solo desarrollo, va documentada en el README

random.seed(2026)


def uid(nombre: str) -> str:
    return str(uuid.uuid5(NS, nombre))


def q(s) -> str:
    if s is None:
        return "NULL"
    return "'" + str(s).replace("'", "''") + "'"


CLINICAS = [
    {"codigo": "central-cartago", "nombre": "Clínica Central Cartago", "tz": "America/Costa_Rica",
     "esp": ["Medicina general", "Odontología", "Psicología"]},
    {"codigo": "san-pedro", "nombre": "Consultorios San Pedro", "tz": "America/Costa_Rica",
     "esp": ["Medicina general", "Fisioterapia", "Nutrición"]},
    {"codigo": "bella-vista", "nombre": "Clínica Bella Vista", "tz": "America/Panama",
     "esp": ["Medicina general", "Odontología", "Fisioterapia"]},
]

SERVICIOS = {
    "Medicina general": [("Consulta general", 20), ("Control de seguimiento", 15)],
    "Odontología": [("Valoración odontológica", 30), ("Limpieza dental", 45)],
    "Psicología": [("Sesión de psicoterapia", 50)],
    "Fisioterapia": [("Sesión de fisioterapia", 40), ("Valoración fisioterapéutica", 30)],
    "Nutrición": [("Consulta nutricional", 30)],
}

NOMBRES = ["Andrea", "Luis", "María José", "Carlos", "Daniela", "José Pablo", "Valeria", "Esteban",
           "Mariana", "Diego", "Fernanda", "Javier", "Gabriela", "Andrés", "Natalia", "Mauricio",
           "Paula", "Ricardo", "Sofía", "Felipe", "Camila", "Adrián", "Laura", "Sebastián"]
APELLIDOS = ["Rojas", "Vargas", "Jiménez", "Mora", "Solano", "Araya", "Quesada", "Chaves", "Brenes",
             "Calderón", "Salas", "Navarro", "Alfaro", "Castro", "Ramírez", "Soto", "Méndez", "Ulate"]

# Jornada partida (RF5): mañana y tarde, lunes a viernes; algunos atienden sábado.
JORNADA = [("08:00", "12:00"), ("13:00", "17:00")]


def nombre_persona():
    return random.choice(NOMBRES), f"{random.choice(APELLIDOS)} {random.choice(APELLIDOS)}"


def main():
    sql = ["-- GENERADO por scripts/generar_semilla.py. No editar a mano.",
           "BEGIN;",
           "INSERT INTO rol (codigo, descripcion) VALUES",
           "  ('admin_plataforma', 'Administrador de la plataforma; da de alta clínicas'),",
           "  ('admin_clinica', 'Administrador de una clínica'),",
           "  ('recepcion', 'Recepción: pacientes y agenda'),",
           "  ('profesional', 'Profesional de salud');"]
    usuarios_kc = []

    def usuario(clave, clinica_id, rol, nombre, apellidos, username):
        sub = uid(f"sub:{clave}")
        uid_db = uid(f"usuario:{clave}")
        sql.append(
            f"INSERT INTO usuario (id, clinica_id, idp_sub, rol_codigo, nombre_visible) VALUES "
            f"({q(uid_db)}, {q(clinica_id) if clinica_id else 'NULL'}, {q(sub)}, {q(rol)}, {q(nombre + ' ' + apellidos)});")
        usuarios_kc.append({
            "id": sub, "username": username, "enabled": True, "emailVerified": True,
            "firstName": nombre, "lastName": apellidos, "email": f"{username}@medicitas.test",
            "credentials": [{"type": "password", "value": CLAVE_PRUEBA, "temporary": False}],
            "realmRoles": [rol],
        })
        return uid_db

    usuario("plataforma", None, "admin_plataforma", "Ana", "Plataforma", "plataforma")

    for c in CLINICAS:
        cid = uid(f"clinica:{c['codigo']}")
        cod = c["codigo"]
        sql.append(f"\n-- ===== {c['nombre']} =====")
        sql.append(f"INSERT INTO clinica (id, codigo, nombre, zona_horaria) VALUES "
                   f"({q(cid)}, {q(cod)}, {q(c['nombre'])}, {q(c['tz'])});")

        usuario(f"{cod}:admin", cid, "admin_clinica", *nombre_persona(), f"admin.{cod}")
        usuario(f"{cod}:recepcion1", cid, "recepcion", *nombre_persona(), f"recepcion.{cod}")
        usuario(f"{cod}:recepcion2", cid, "recepcion", *nombre_persona(), f"recepcion2.{cod}")

        servicios_por_esp = {}
        for esp in c["esp"]:
            eid = uid(f"esp:{cod}:{esp}")
            sql.append(f"INSERT INTO especialidad (id, clinica_id, nombre) VALUES ({q(eid)}, {q(cid)}, {q(esp)});")
            servicios_por_esp[esp] = []
            for nombre_srv, dur in SERVICIOS[esp]:
                sid = uid(f"srv:{cod}:{nombre_srv}")
                servicios_por_esp[esp].append(sid)
                sql.append(f"INSERT INTO servicio (id, clinica_id, nombre, duracion_min, especialidad_id) VALUES "
                           f"({q(sid)}, {q(cid)}, {q(nombre_srv)}, {dur}, {q(eid)});")

        # Dos profesionales de medicina general y uno por cada otra especialidad.
        plantilla = [c["esp"][0]] + c["esp"]
        for i, esp in enumerate(plantilla, start=1):
            nom, ape = nombre_persona()
            u = usuario(f"{cod}:prof{i}", cid, "profesional", nom, ape, f"prof{i}.{cod}")
            pid = uid(f"prof:{cod}:{i}")
            eid = uid(f"esp:{cod}:{esp}")
            titulo = "Dra." if nom.endswith("a") else "Dr."
            sql.append(f"INSERT INTO profesional (id, clinica_id, usuario_id, nombre, especialidad_id) VALUES "
                       f"({q(pid)}, {q(cid)}, {q(u)}, {q(f'{titulo} {nom} {ape}')}, {q(eid)});")
            for sid in servicios_por_esp[esp]:
                sql.append(f"INSERT INTO profesional_servicio (clinica_id, profesional_id, servicio_id) VALUES "
                           f"({q(cid)}, {q(pid)}, {q(sid)});")
            dias = [1, 2, 3, 4, 5] + ([6] if i % 2 == 0 else [])
            for d in dias:
                bloques = JORNADA if d <= 5 else [("08:00", "12:00")]
                for ini, fin in bloques:
                    sql.append(f"INSERT INTO horario_atencion (clinica_id, profesional_id, dia_semana, hora_inicio, hora_fin) "
                               f"VALUES ({q(cid)}, {q(pid)}, {d}, {q(ini)}, {q(fin)});")

        for n in range(1, 41):
            nom, ape = nombre_persona()
            pac = uid(f"pac:{cod}:{n}")
            ced = f"{random.randint(1, 7)}-{random.randint(100, 999):04d}-{random.randint(1000, 9999)}"
            anio = random.randint(1950, 2020)
            fecha = f"{anio}-{random.randint(1, 12):02d}-{random.randint(1, 28):02d}"
            tel = f"{random.choice(['8', '7', '6'])}{random.randint(1000000, 9999999)}"
            correo = f"{nom.split()[0].lower()}.{ape.split()[0].lower()}{n}@correo.test"
            correo = correo.replace("á", "a").replace("é", "e").replace("í", "i").replace("ó", "o").replace("ú", "u")
            sql.append(f"INSERT INTO paciente (id, clinica_id, identificacion, nombre, apellidos, fecha_nacimiento, telefono, correo) "
                       f"VALUES ({q(pac)}, {q(cid)}, {q(ced)}, {q(nom)}, {q(ape)}, {q(fecha)}, {q(tel)}, {q(correo)});")

    sql.append(CITAS_DO)
    sql.append("COMMIT;")
    (RAIZ / "db/init/05_semilla.sql").write_text("\n".join(sql) + "\n", encoding="utf-8")

    realm = json.loads((RAIZ / "keycloak/realm-base.json").read_text(encoding="utf-8"))
    realm["users"] = usuarios_kc + realm.get("users", [])
    (RAIZ / "keycloak/medicitas-realm.json").write_text(
        json.dumps(realm, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"semilla: {len(sql)} líneas, {len(usuarios_kc)} usuarios en el realm")


# Citas alrededor de "hoy": recorre cada profesional y cada día hábil, ocupa
# más o menos la mitad de los espacios y deja pasado con desenlaces y notas.
CITAS_DO = r"""
-- ===== Citas (se generan al levantar, relativas a la fecha actual) =====
DO $$
DECLARE
  p record; h record; s record;
  d date; t timestamp; ini timestamptz;
  pac uuid; rec uuid; nueva uuid;
  r double precision;
BEGIN
  PERFORM setseed(0.2026);
  FOR p IN SELECT pr.id, pr.clinica_id, pr.usuario_id, c.zona_horaria AS tz
             FROM profesional pr JOIN clinica c ON c.id = pr.clinica_id ORDER BY pr.id LOOP
    SELECT u.id INTO rec FROM usuario u
     WHERE u.clinica_id = p.clinica_id AND u.rol_codigo = 'recepcion' ORDER BY u.id LIMIT 1;
    FOR d IN SELECT generate_series(current_date - 45, current_date + 21, interval '1 day')::date LOOP
      FOR h IN SELECT * FROM horario_atencion WHERE profesional_id = p.id
                 AND dia_semana = extract(isodow FROM d) ORDER BY hora_inicio LOOP
        t := d + h.hora_inicio;
        WHILE t < d + h.hora_fin LOOP
          SELECT sv.id, sv.duracion_min INTO s FROM profesional_servicio ps JOIN servicio sv ON sv.id = ps.servicio_id
           WHERE ps.profesional_id = p.id ORDER BY random() LIMIT 1;
          EXIT WHEN t + make_interval(mins => s.duracion_min) > d + h.hora_fin;
          IF random() < 0.55 THEN
            SELECT id INTO pac FROM paciente WHERE clinica_id = p.clinica_id ORDER BY random() LIMIT 1;
            ini := t AT TIME ZONE p.tz;
            BEGIN
              PERFORM set_config('app.usuario_id', rec::text, true);
              INSERT INTO cita (clinica_id, paciente_id, profesional_id, servicio_id, inicio, fin, creada_por)
              VALUES (p.clinica_id, pac, p.id, s.id, ini, ini, rec) RETURNING id INTO nueva;
              r := random();
              IF ini < now() THEN
                IF r < 0.75 THEN
                  PERFORM set_config('app.usuario_id', p.usuario_id::text, true);
                  UPDATE cita SET estado = 'atendida' WHERE id = nueva;
                  IF random() < 0.6 THEN
                    INSERT INTO nota_clinica (clinica_id, cita_id, profesional_id, contenido)
                    VALUES (p.clinica_id, nueva, p.id,
                            (ARRAY['Paciente refiere mejoría. Se mantiene tratamiento y control en un mes.',
                                   'Se indica reposo relativo y analgesia. Revalorar si persisten síntomas.',
                                   'Evolución estable. Se ajusta plan y se dan recomendaciones por escrito.',
                                   'Primera valoración. Se solicitan exámenes de laboratorio de control.'])[1 + floor(random() * 4)::int]);
                  END IF;
                ELSIF r < 0.88 THEN
                  UPDATE cita SET estado = 'ausente' WHERE id = nueva;
                ELSE
                  UPDATE cita SET estado = 'cancelada', motivo_cancelacion = 'Paciente avisó que no podía asistir' WHERE id = nueva;
                END IF;
              ELSIF r < 0.08 THEN
                UPDATE cita SET estado = 'cancelada', motivo_cancelacion = 'Cancelada por el paciente por teléfono' WHERE id = nueva;
              END IF;
            EXCEPTION WHEN exclusion_violation THEN
              NULL;  -- el paciente ya tenía otra cita a esa hora; se deja el espacio libre
            END;
          END IF;
          t := t + make_interval(mins => s.duracion_min);
        END LOOP;
      END LOOP;
    END LOOP;
  END LOOP;
  PERFORM set_config('app.usuario_id', '', true);
END $$;
"""

if __name__ == "__main__":
    main()
