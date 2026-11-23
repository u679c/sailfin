FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY browser-agent ./browser-agent
COPY cmd/core ./cmd/core
COPY internal ./internal
ARG SAILFIN_VERSION
RUN CGO_ENABLED=1 go build -ldflags "-X sailfin/internal/branding.packageVersion=${SAILFIN_VERSION}" -trimpath -o /sailfin-core ./cmd/core

FROM debian:bookworm-slim
COPY --from=build /sailfin-core /usr/local/bin/sailfin-core
COPY web /opt/sailfin/web
ENTRYPOINT ["/usr/local/bin/sailfin-core"]
CMD ["container-serve", "--addr", "0.0.0.0:8080", "--web-dir", "/opt/sailfin/web"]
