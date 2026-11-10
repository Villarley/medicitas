// Package config lee la configuración desde variables de entorno (.env).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Puerto              string
	DatabaseURL         string
	RedisURL            string
	CacheTTL            time.Duration
	CachePermitirBypass bool
	OIDCIssuer          string
	OIDCJWKSURL         string
	OIDCAudiencia       string
	KeycloakURLInterna  string
	KeycloakRealm       string
	KeycloakClientID    string
	KeycloakSecret      string
}

func Cargar() (Config, error) {
	c := Config{
		Puerto:              valor("PUERTO", "8000"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		RedisURL:            valor("REDIS_URL", "redis://redis:6379/0"),
		CachePermitirBypass: os.Getenv("CACHE_PERMITIR_BYPASS") == "1",
		OIDCIssuer:          os.Getenv("OIDC_ISSUER"),
		OIDCJWKSURL:         os.Getenv("OIDC_JWKS_URL"),
		OIDCAudiencia:       valor("OIDC_AUDIENCIA", "medicitas-api"),
		KeycloakURLInterna:  valor("KEYCLOAK_URL_INTERNA", "http://keycloak:8080"),
		KeycloakRealm:       valor("KEYCLOAK_REALM", "medicitas"),
		KeycloakClientID:    valor("KEYCLOAK_API_CLIENT_ID", "medicitas-api"),
		KeycloakSecret:      os.Getenv("KEYCLOAK_API_CLIENT_SECRET"),
	}
	ttl, err := strconv.Atoi(valor("CACHE_TTL_SEGUNDOS", "300"))
	if err != nil || ttl <= 0 {
		return c, fmt.Errorf("CACHE_TTL_SEGUNDOS inválido")
	}
	c.CacheTTL = time.Duration(ttl) * time.Second
	for nombre, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "OIDC_ISSUER": c.OIDCIssuer, "OIDC_JWKS_URL": c.OIDCJWKSURL} {
		if v == "" {
			return c, fmt.Errorf("falta la variable %s", nombre)
		}
	}
	return c, nil
}

func valor(nombre, porDefecto string) string {
	if v := os.Getenv(nombre); v != "" {
		return v
	}
	return porDefecto
}
