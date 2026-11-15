// Package http arma el router y los manejadores de la API REST.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"medicitas/api/internal/db"
	"medicitas/api/internal/errores"
	"medicitas/api/internal/identidad"
)

type cuerpoError struct {
	Error struct {
		Codigo  string `json:"codigo"`
		Mensaje string `json:"mensaje"`
	} `json:"error"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func Error(w http.ResponseWriter, err error) {
	e := errores.Como(db.Traducir(err))
	if e == nil {
		slog.Error("error no esperado", "error", err)
		e = errores.Interno()
	}
	var c cuerpoError
	c.Error.Codigo, c.Error.Mensaje = e.Codigo, e.Mensaje
	JSON(w, e.Status, c)
}

func LeerJSON(r *http.Request, destino any) error {
	cuerpo, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return errores.Invalido("No se pudo leer el cuerpo")
	}
	if len(strings.TrimSpace(string(cuerpo))) == 0 {
		return errores.Invalido("El cuerpo está vacío")
	}
	if err := json.Unmarshal(cuerpo, destino); err != nil {
		var tipo *json.UnmarshalTypeError
		if errors.As(err, &tipo) {
			return errores.Invalido("Campo con tipo inválido: " + tipo.Field)
		}
		return errores.Invalido("JSON inválido o con formato de fecha/UUID incorrecto")
	}
	return nil
}

func ParamUUID(r *http.Request, nombre string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, nombre))
	if err != nil {
		return uuid.Nil, errores.Invalido(nombre + " no es un UUID válido")
	}
	return id, nil
}

func QueryUUID(r *http.Request, nombre string) (uuid.UUID, error) {
	v := r.URL.Query().Get(nombre)
	if v == "" {
		return uuid.Nil, errores.Invalido("Falta el parámetro " + nombre)
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, errores.Invalido(nombre + " no es un UUID válido")
	}
	return id, nil
}

func Ident(r *http.Request) identidad.Identidad {
	i, _ := identidad.DeContexto(r.Context())
	return i
}

func recuperar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic en manejador", "valor", p, "ruta", r.URL.Path)
				Error(w, errores.Interno())
			}
		}()
		next.ServeHTTP(w, r)
	})
}
