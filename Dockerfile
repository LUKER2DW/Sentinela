# Sentinela: one image, two roles (`serve` and `record`, ADR-0007).
# Base images pinned by digest (AGENTS.md §2, SECURITY.md rule 7).

FROM node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-trixie@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/embed.go ./web/embed.go
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/sentinela ./cmd/sentinela

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a
# ffmpeg/ffprobe from Debian: copy-only recording, audio-only AAC (ADR-0006).
RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --uid 1000 --user-group --no-create-home --shell /usr/sbin/nologin sentinela
COPY --from=go /out/sentinela /usr/local/bin/sentinela
USER 1000:1000
ENV TZ=UTC SENTINELA_CONFIG=/etc/sentinela/config.yaml
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sentinela"]
CMD ["serve"]
