// Comando medircache compara la consulta de disponibilidad (RF7) sin caché y con caché.
//
//	docker compose exec api go run ./cmd/medircache
//
// Hace las mismas consultas dos veces: primero con el encabezado X-Sin-Cache: 1
// (la API lo respeta solo si CACHE_PERMITIR_BYPASS=1) y después normal.
// Imprime una tabla markdown lista para docs/cache.md.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const repeticiones = 300

func env(n, d string) string {
	if v := os.Getenv(n); v != "" {
		return v
	}
	return d
}

func main() {
	api := env("API_URL", "http://localhost:8000")
	kc := env("KEYCLOAK_URL_INTERNA", "http://keycloak:8080")
	token, err := pedirToken(kc, "recepcion.central-cartago", env("CLAVE_USUARIOS_PRUEBA", "Medicitas.2026"))
	salirSi(err)
	combos, err := combinaciones(env("DATABASE_URL", ""), token)
	salirSi(err)

	cliente := &http.Client{Timeout: 10 * time.Second}
	medir := func(sinCache bool) (tiempos []float64, aciertos int) {
		for i := 0; i < repeticiones; i++ {
			c := combos[i%len(combos)]
			req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/disponibilidad?profesional_id=%s&servicio_id=%s&fecha=%s", api, c[0], c[1], c[2]), nil)
			req.Header.Set("Authorization", "Bearer "+token)
			if sinCache {
				req.Header.Set("X-Sin-Cache", "1")
			}
			ini := time.Now()
			resp, err := cliente.Do(req)
			salirSi(err)
			var cuerpo struct {
				Fuente string `json:"fuente"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&cuerpo)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				salirSi(fmt.Errorf("respuesta %d", resp.StatusCode))
			}
			tiempos = append(tiempos, float64(time.Since(ini).Microseconds())/1000)
			if cuerpo.Fuente == "cache" {
				aciertos++
			}
		}
		return
	}
	sin, aSin := medir(true)
	con, aCon := medir(false)
	fmt.Printf("Consultas de disponibilidad (RF7): %d por escenario, %d combinaciones profesional/fecha\n\n", repeticiones, len(combos))
	fmt.Println("| Escenario | Promedio (ms) | p50 (ms) | p95 (ms) | Aciertos de caché |")
	fmt.Println("|---|---|---|---|---|")
	fila := func(n string, t []float64, a int) {
		fmt.Printf("| %s | %.2f | %.2f | %.2f | %d%% |\n", n, promedio(t), pct(t, 50), pct(t, 95), a*100/len(t))
	}
	fila("Sin caché (solo PostgreSQL)", sin, aSin)
	fila("Con caché (Redis)", con, aCon)
}

// combinaciones toma 10 (profesional, servicio, fecha) de la semilla de Central Cartago, en días hábiles próximos.
func combinaciones(dbURL, token string) ([][3]string, error) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// La clínica sale de la identidad del token, igual que en la API.
	var clinica string
	if err := tx.QueryRow(ctx, `SELECT clinica_id::text FROM resolver_identidad($1)`, subDe(token)).Scan(&clinica); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE db_recepcion"); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.clinica_id', $1, true)`, clinica); err != nil {
		return nil, err
	}
	filas, err := tx.Query(ctx, `SELECT ps.profesional_id::text, ps.servicio_id::text FROM profesional_servicio ps ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	pares, err := pgx.CollectRows(filas, func(f pgx.CollectableRow) ([2]string, error) {
		var p [2]string
		return p, f.Scan(&p[0], &p[1])
	})
	if err != nil {
		return nil, err
	}
	var out [][3]string
	dia := time.Now()
	for len(out) < 10 && len(pares) > 0 {
		dia = dia.AddDate(0, 0, 1)
		if dia.Weekday() == time.Saturday || dia.Weekday() == time.Sunday {
			continue
		}
		p := pares[len(out)%len(pares)]
		out = append(out, [3]string{p[0], p[1], dia.Format("2006-01-02")})
	}
	return out, nil
}

// subDe lee el sub del token sin verificarlo (solo para elegir datos de la medición).
func subDe(token string) string {
	partes := strings.Split(token, ".")
	if len(partes) != 3 {
		return ""
	}
	b, _ := base64.RawURLEncoding.DecodeString(partes[1])
	var c struct {
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(b, &c)
	return c.Sub
}

func pedirToken(kc, usuario, clave string) (string, error) {
	resp, err := http.PostForm(kc+"/realms/medicitas/protocol/openid-connect/token", url.Values{
		"grant_type": {"password"}, "client_id": {"medicitas-cli"}, "username": {usuario}, "password": {clave}})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var c struct {
		AccessToken string `json:"access_token"`
	}
	return c.AccessToken, json.NewDecoder(resp.Body).Decode(&c)
}

func promedio(t []float64) float64 {
	s := 0.0
	for _, x := range t {
		s += x
	}
	return s / float64(len(t))
}

func pct(t []float64, p int) float64 {
	c := append([]float64(nil), t...)
	sort.Float64s(c)
	return c[(len(c)-1)*p/100]
}

func salirSi(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
