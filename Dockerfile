FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/dns-proxy-server ./cmd/dns-proxy-server

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/dns-proxy-server /app/dns-proxy-server
COPY config.example.yaml /app/config.example.yaml
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/dns-proxy-server"]
CMD ["-config", "/app/config.example.yaml"]
