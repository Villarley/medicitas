// Comando api levanta el servicio REST de MediCitas.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // zonas horarias dentro de la imagen alpine

	"medicitas/api/internal/auth"
	"medicitas/api/internal/cache"
	"medicitas/api/internal/citas"
	"medicitas/api/internal/config"
	"medicitas/api/internal/db"
	"medicitas/api/internal/disponibilidad"
	apihttp "medicitas/api/internal/http"
	"medicitas/api/internal/keycloak"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := correr(); err != nil {
		slog.Error("la API se detuvo", "error", err)
		os.Exit(1)
	}
}

func correr() error {
	cfg, err := config.Cargar()
	if err != nil {
		return err
	}
	ctx, parar := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer parar()

	store, err := db.Conectar(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Pool.Close()

	cch, err := cache.Nueva(cfg.RedisURL, cfg.CacheTTL)
	if err != nil {
		return err
	}
	validador, err := auth.NuevoValidador(ctx, cfg.OIDCJWKSURL, cfg.OIDCIssuer, cfg.OIDCAudiencia)
	if err != nil {
		return err
	}

	deps := &apihttp.Deps{
		Store:               store,
		Cache:               cch,
		Validador:           validador,
		Keycloak:            keycloak.NuevoAdmin(cfg.KeycloakURLInterna, cfg.KeycloakRealm, cfg.KeycloakClientID, cfg.KeycloakSecret),
		Disponibilidad:      &disponibilidad.Servicio{Store: store, Cache: cch},
		Citas:               &citas.Servicio{Store: store, Cache: cch},
		CachePermitirBypass: cfg.CachePermitirBypass,
	}
	srv := &http.Server{Addr: ":" + cfg.Puerto, Handler: apihttp.Router(deps), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		<-ctx.Done()
		apagar, cancelar := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelar()
		_ = srv.Shutdown(apagar)
	}()
	slog.Info("MediCitas escuchando", "puerto", cfg.Puerto)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
