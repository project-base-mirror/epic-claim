FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /epic-claim ./cmd/epic-claim

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /epic-claim /usr/local/bin/epic-claim
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["epic-claim", "-config", "/app/config.yaml"]
