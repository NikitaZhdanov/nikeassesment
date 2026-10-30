FROM golang:1.27.2-bookworm AS source
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM source AS test
CMD ["go", "test", "./..."]

FROM source AS build
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /catalog ./cmd/catalog

FROM debian:bookworm-slim AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /catalog /usr/local/bin/catalog
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/catalog"]
