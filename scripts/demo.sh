#!/usr/bin/env bash
# Recorrido de una cita de punta a punta contra la API (guion del video).
# Uso, con la pila arriba:  ./scripts/demo.sh
# Necesita curl y jq (en macOS: brew install jq).
set -euo pipefail

API=${API_URL:-http://localhost:8000}/api/v1
KC=${KEYCLOAK_URL:-http://localhost:8080}/realms/medicitas/protocol/openid-connect/token
CLAVE=${CLAVE_USUARIOS_PRUEBA:-Medicitas.2026}

token() {
  curl -s -X POST "$KC" -d grant_type=password -d client_id=medicitas-cli \
       -d username="$1" -d password="$CLAVE" | jq -r .access_token
}
paso() { printf '\n\033[1m== %s\033[0m\n' "$1"; }
pedir() { # metodo ruta token [cuerpo]
  local salida
  salida=$(curl -s -w '\n%{http_code}' -X "$1" "$API$2" -H "Authorization: Bearer $3" \
           -H 'Content-Type: application/json' ${4:+-d "$4"})
  CODIGO=$(tail -n1 <<<"$salida"); CUERPO=$(sed '$d' <<<"$salida")
  echo "HTTP $CODIGO"; jq -C . <<<"$CUERPO" 2>/dev/null | head -25 || echo "$CUERPO"
}

paso "Sin token la API responde 401"
pedir GET "/disponibilidad" ""

REC=$(token recepcion.central-cartago)
PROF_TOKEN=$(token prof1.central-cartago)

paso "Recepción busca el primer día hábil con espacio para el profesional 1"
sub_de() { # lee el sub de un token (solo para buscar datos del demo)
  local p; p=$(cut -d. -f2 <<<"$1" | tr '_-' '/+'); while [ $(( ${#p} % 4 )) -ne 0 ]; do p="$p="; done
  base64 -d <<<"$p" 2>/dev/null | jq -r .sub
}
PROF=$(docker compose exec -T db psql -U postgres -d medicitas -Atc \
       "select p.id from profesional p join usuario u on u.id = p.usuario_id where u.idp_sub = '$(sub_de "$PROF_TOKEN")'")
SERV=$(docker compose exec -T db psql -U postgres -d medicitas -Atc \
       "select servicio_id from profesional_servicio where profesional_id = '$PROF' limit 1")
PAC=$(docker compose exec -T db psql -U postgres -d medicitas -Atc \
       "select id from paciente where clinica_id = (select clinica_id from profesional where id = '$PROF') order by apellidos limit 1")
for d in $(seq 1 14); do
  FECHA=$(date -v+${d}d +%F 2>/dev/null || date -d "+$d day" +%F)
  pedir GET "/disponibilidad?profesional_id=$PROF&servicio_id=$SERV&fecha=$FECHA" "$REC" >/dev/null
  N=$(jq '.espacios | length' <<<"$CUERPO")
  [ "$N" -gt 1 ] && break
done
pedir GET "/disponibilidad?profesional_id=$PROF&servicio_id=$SERV&fecha=$FECHA" "$REC"
INICIO=$(jq -r '.espacios[0].inicio' <<<"$CUERPO")
OTRO=$(jq -r '.espacios[1].inicio' <<<"$CUERPO")

paso "Reserva el primer espacio ($INICIO): 201"
pedir POST "/citas" "$REC" "{\"paciente_id\":\"$PAC\",\"profesional_id\":\"$PROF\",\"servicio_id\":\"$SERV\",\"inicio\":\"$INICIO\"}"
CITA=$(jq -r .id <<<"$CUERPO")

paso "Otra reserva al mismo espacio: 409 ESPACIO_TOMADO"
PAC2=$(docker compose exec -T db psql -U postgres -d medicitas -Atc \
       "select id from paciente where clinica_id = (select clinica_id from profesional where id = '$PROF') order by apellidos offset 1 limit 1")
pedir POST "/citas" "$REC" "{\"paciente_id\":\"$PAC2\",\"profesional_id\":\"$PROF\",\"servicio_id\":\"$SERV\",\"inicio\":\"$INICIO\"}"

paso "La disponibilidad ya no ofrece ese espacio (caché invalidada)"
pedir GET "/disponibilidad?profesional_id=$PROF&servicio_id=$SERV&fecha=$FECHA" "$REC" >/dev/null
jq '{fuente, primer_espacio: .espacios[0].inicio}' <<<"$CUERPO"

paso "Reprograma la cita a $OTRO: 201 con cita nueva"
pedir POST "/citas/$CITA/reprogramar" "$REC" "{\"inicio\":\"$OTRO\"}"
NUEVA=$(jq -r .id <<<"$CUERPO")

paso "La cita vieja quedó reprogramada"
pedir GET "/citas/$CITA" "$REC"

paso "El profesional intenta registrar el desenlace antes de la hora: 422"
pedir POST "/citas/$NUEVA/desenlace" "$PROF_TOKEN" '{"desenlace":"atendida"}'

paso "Recepción cancela con motivo: 200"
pedir POST "/citas/$NUEVA/cancelar" "$REC" '{"motivo":"El paciente pidió cancelar por teléfono"}'

paso "Cancelar otra vez: 409 TRANSICION_INVALIDA"
pedir POST "/citas/$NUEVA/cancelar" "$REC" '{"motivo":"otra vez"}'

paso "Una cita de la semana pasada: el profesional registra el desenlace"
PASADA=$(docker compose exec -T db psql -U postgres -d medicitas -Atc \
  "insert into cita (clinica_id, paciente_id, profesional_id, servicio_id, inicio, fin, creada_por)
   select p.clinica_id, '$PAC', p.id, '$SERV', h.ts, h.ts, (select id from usuario where clinica_id = p.clinica_id and rol_codigo = 'recepcion' limit 1)
     from profesional p,
          lateral (select ((current_date - 7 * k) + ha.hora_inicio) at time zone 'America/Costa_Rica' as ts
                     from generate_series(1, 4) k, horario_atencion ha
                    where ha.profesional_id = p.id and ha.dia_semana = extract(isodow from current_date - 7 * k)
                    order by k limit 1) h
    where p.id = '$PROF'
   on conflict do nothing returning id" | head -1)
if [ -n "$PASADA" ]; then
  pedir POST "/citas/$PASADA/desenlace" "$PROF_TOKEN" '{"desenlace":"atendida"}'
else
  echo "(no se pudo crear la cita pasada de ejemplo; el espacio ya estaba ocupado)"
fi

paso "Listo. La prueba de carrera se corre con:"
echo "docker compose exec api go test -count=1 -run Carrera -v ./pruebas/"
