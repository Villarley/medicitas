// Package keycloak habla con la Admin REST API de Keycloak usando la cuenta de
// servicio del client medicitas-api. Lo usan el alta de clínicas y la
// gestión de usuarios. No emite ni firma tokens de usuario: eso es de Keycloak.
package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Admin struct {
	base, realm, clientID, secret string
	http                          *http.Client

	mu    sync.Mutex
	token string
	vence time.Time
}

func NuevoAdmin(base, realm, clientID, secret string) *Admin {
	return &Admin{base: strings.TrimRight(base, "/"), realm: realm, clientID: clientID, secret: secret,
		http: &http.Client{Timeout: 10 * time.Second}}
}

type NuevoUsuario struct {
	Username  string
	Nombre    string
	Apellidos string
	Correo    string
	Clave     string
	Rol       string // uno de los 4 roles de realm
}

func (a *Admin) tokenServicio(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.vence) {
		return a.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {a.clientID}, "client_secret": {a.secret}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", a.base, a.realm), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keycloak token: %s", resp.Status)
	}
	var cuerpo struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cuerpo); err != nil {
		return "", err
	}
	a.token = cuerpo.AccessToken
	a.vence = time.Now().Add(time.Duration(cuerpo.ExpiresIn-30) * time.Second)
	return a.token, nil
}

func (a *Admin) pedir(ctx context.Context, metodo, ruta string, cuerpo any) (*http.Response, error) {
	tok, err := a.tokenServicio(ctx)
	if err != nil {
		return nil, err
	}
	var r io.Reader
	if cuerpo != nil {
		b, err := json.Marshal(cuerpo)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, metodo, fmt.Sprintf("%s/admin/realms/%s%s", a.base, a.realm, ruta), r)
	req.Header.Set("Authorization", "Bearer "+tok)
	if cuerpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return a.http.Do(req)
}

var ErrUsuarioExiste = fmt.Errorf("el usuario ya existe en el proveedor de identidad")

func (a *Admin) CrearUsuario(ctx context.Context, u NuevoUsuario) (string, error) {
	resp, err := a.pedir(ctx, http.MethodPost, "/users", map[string]any{
		"username": u.Username, "enabled": true, "emailVerified": true,
		"firstName": u.Nombre, "lastName": u.Apellidos, "email": u.Correo,
		"credentials": []map[string]any{{"type": "password", "value": u.Clave, "temporary": false}},
	})
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return "", ErrUsuarioExiste
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("keycloak crear usuario: %s", resp.Status)
	}
	ubicacion := resp.Header.Get("Location")
	id := ubicacion[strings.LastIndex(ubicacion, "/")+1:]
	if err := a.asignarRol(ctx, id, u.Rol, http.MethodPost); err != nil {
		_ = a.BorrarUsuario(ctx, id)
		return "", err
	}
	return id, nil
}

func (a *Admin) rol(ctx context.Context, nombre string) (map[string]any, error) {
	resp, err := a.pedir(ctx, http.MethodGet, "/roles/"+url.PathEscape(nombre), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("keycloak rol %s: %s", nombre, resp.Status)
	}
	var r map[string]any
	return r, json.NewDecoder(resp.Body).Decode(&r)
}

func (a *Admin) asignarRol(ctx context.Context, id, rol, metodo string) error {
	r, err := a.rol(ctx, rol)
	if err != nil {
		return err
	}
	resp, err := a.pedir(ctx, metodo, "/users/"+url.PathEscape(id)+"/role-mappings/realm", []map[string]any{r})
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("keycloak rol de usuario: %s", resp.Status)
	}
	return nil
}

func (a *Admin) CambiarRol(ctx context.Context, id, anterior, nuevo string) error {
	if err := a.asignarRol(ctx, id, anterior, http.MethodDelete); err != nil {
		return err
	}
	return a.asignarRol(ctx, id, nuevo, http.MethodPost)
}

func (a *Admin) Deshabilitar(ctx context.Context, id string) error {
	resp, err := a.pedir(ctx, http.MethodPut, "/users/"+url.PathEscape(id), map[string]any{"enabled": false})
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("keycloak deshabilitar: %s", resp.Status)
	}
	return nil
}

func (a *Admin) BorrarUsuario(ctx context.Context, id string) error {
	resp, err := a.pedir(ctx, http.MethodDelete, "/users/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("keycloak borrar: %s", resp.Status)
	}
	return nil
}
