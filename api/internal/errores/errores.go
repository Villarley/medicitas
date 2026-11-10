// Package errores define el error de dominio que la API devuelve como JSON.
package errores

import (
	"errors"
	"net/http"
)

// Error lleva el código HTTP y el código de negocio que ve el cliente.
type Error struct {
	Status  int
	Codigo  string
	Mensaje string
}

func (e *Error) Error() string { return e.Codigo + ": " + e.Mensaje }

func Nuevo(status int, codigo, mensaje string) *Error {
	return &Error{Status: status, Codigo: codigo, Mensaje: mensaje}
}

func NoAutenticado(m string) *Error { return Nuevo(http.StatusUnauthorized, "NO_AUTENTICADO", m) }
func Prohibido(m string) *Error     { return Nuevo(http.StatusForbidden, "PROHIBIDO", m) }
func NoEncontrado(m string) *Error  { return Nuevo(http.StatusNotFound, "NO_ENCONTRADO", m) }
func Invalido(m string) *Error      { return Nuevo(http.StatusUnprocessableEntity, "SOLICITUD_INVALIDA", m) }
func Conflicto(c, m string) *Error  { return Nuevo(http.StatusConflict, c, m) }
func Reintentar() *Error {
	return Nuevo(http.StatusServiceUnavailable, "REINTENTAR", "La operación chocó con otra; intentá de nuevo")
}
func Interno() *Error { return Nuevo(http.StatusInternalServerError, "ERROR_INTERNO", "Error interno") }

// Como devuelve el *Error que haya en la cadena, o nil.
func Como(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}
