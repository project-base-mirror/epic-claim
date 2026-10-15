FROM golang:1.24.13-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /epic-claim ./cmd/epic-claim

# Browser and package versions must match. No stealth or challenge solver.
FROM mcr.microsoft.com/playwright:v1.55.1-noble
ENV NODE_ENV=production EPIC_PROFILE_ROOT=/data/profiles
# Manual login live view: X framebuffer capture (ffmpeg x11grab) and X input (xdotool).
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg xdotool && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY workers/epic/package*.json ./workers/epic/
RUN cd workers/epic && npm ci --omit=dev --ignore-scripts && npm cache clean --force
COPY workers/epic ./workers/epic
COPY --from=build /epic-claim /usr/local/bin/epic-claim
COPY config.docker.yaml /app/config.yaml
RUN mkdir -p /data/profiles && chown -R pwuser:pwuser /data /app
USER pwuser
EXPOSE 8080
ENTRYPOINT ["epic-claim", "-config", "/app/config.yaml"]
