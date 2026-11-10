# La imagen conserva el toolchain y el código a propósito: así las pruebas
# corren dentro del contenedor con `docker compose exec api go test -count=1 ./...`.
FROM golang:1.25-alpine
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /usr/local/bin/medicitas ./cmd/api
EXPOSE 8000
HEALTHCHECK --interval=5s --timeout=3s --retries=20 CMD wget -qO- http://localhost:8000/api/v1/salud || exit 1
CMD ["medicitas"]
